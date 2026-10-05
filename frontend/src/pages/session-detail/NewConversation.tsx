// The new-conversation composer. There is no session yet: the first
// submission creates one at the selected machine + target (worktree or
// current checkout) and sends the prompt in the same request, so the id,
// directory and machine never change under the user afterwards.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, postJSON, type PrepareSessionResponse, type StartSessionRequest } from '../../lib/api';
import type { TargetCandidate } from '../../lib/api.types';
import { useApiStore } from '../../lib/apiStore';
import { BUILTIN_COMMANDS } from '../../lib/commands/builtinCommands';
import { clearDraft } from '../../lib/composerDraft';
import { shortPath } from '../../lib/format';
import { useHeaderInfo } from '../../lib/headerContext';
import { recordFailedSend } from '../../lib/failedSends';
import { launchProgressReporter } from '../../lib/launchProgressStore';
import { NEW_SESSION_ID, newSessionPath, type NewSessionParams } from '../../lib/newSessionPath';
import { getProjectModel, saveProjectModel } from '../../lib/projectModel';
import { remoteLog } from '../../lib/remoteLog';
import { agentModelRef, formatModelRef } from '../../lib/sessionStatus';
import { useUiStore } from '../../lib/uiStore';
import { useOpencodeLaunch, usePlatformCapabilities } from '../../lib/useCapabilities';
import { projectRootForDirectory } from '../../lib/worktrees';
import { Composer, type ComposerHandle } from '../../components/assistant/Composer';
import type { AttachedImage } from '../../components/assistant/useComposerAttachments';
import type { SessionTarget } from '../../components/assistant/ComposerSelectorRow';
import { InlineAlert } from '../../components/InlineAlert';
import { LaunchProgressCard } from '../../components/LaunchProgressCard';
import { useWorktreeEligibility } from './useWorktreeEligibility';
import { startFirstSubmission } from './firstSubmission';
import { sendFirstFiles } from './sendFirstFiles';

export interface NewConversationProps {
  params: NewSessionParams;
  whisperAvailable: boolean;
  composerRef: React.Ref<ComposerHandle>;
  navigate: (path: string) => void;
  navigateToSession: (id: string) => void;
}

export function NewConversation({ params, whisperAvailable, composerRef, navigate, navigateToSession }: NewConversationProps) {
  const { directory, title } = params;
  const remoteId = params.remoteId || 'local';
  const seedNewSession = useApiStore((state) => state.seedNewSession);
  const openWorktreeForm = useUiStore((state) => state.openWorktreeForm);
  const worktreesCapable = useOpencodeLaunch(remoteId);
  const eligibility = useWorktreeEligibility(directory, remoteId);

  const [catalog, setCatalog] = useState<PrepareSessionResponse>();
  const [catalogError, setCatalogError] = useState('');
  const [catalogAttempt, setCatalogAttempt] = useState(0);
  const platform = catalog?.platform || params.platform;
  const caps = usePlatformCapabilities(platform);

  // Header shows the project (worktrees fold to their main checkout).
  const { setInfo } = useHeaderInfo();
  useEffect(() => {
    setInfo({
      sessionId: NEW_SESSION_ID,
      sessionProject: shortPath(projectRootForDirectory(directory)),
      sessionProjectFull: directory,
      sessionRemoteId: remoteId,
    });
    return () => setInfo({});
  }, [directory, remoteId, setInfo]);

  // Prepare only reads catalogs (a running instance's, else history); it
  // never launches OpenCode, so picking a machine starts nothing there.
  useEffect(() => {
    const controller = new AbortController();
    setCatalog(undefined);
    setCatalogError('');
    api.prepareSession({ directory, remoteId, platform: params.platform }, controller.signal).then((result) => {
      if (controller.signal.aborted) return;
      setCatalog(result);
    }).catch((err) => {
      if (controller.signal.aborted) return;
      setCatalogError(err instanceof Error ? err.message : String(err));
    });
    return () => controller.abort();
  }, [directory, remoteId, params.platform, catalogAttempt]);

  const [selectedModel, setSelectedModel] = useState('');
  const [selectedAgent, setSelectedAgent] = useState('');
  const [selectedReasoning, setSelectedReasoning] = useState('');
  const [target, setTarget] = useState<SessionTarget>('worktree');
  const [error, setError] = useState('');
  const inFlight = useRef<number | undefined>(undefined);
  const active = useRef(false);
  const generation = useRef(0);
  useEffect(() => {
    generation.current++;
    active.current = true;
    return () => { active.current = false; };
  }, [directory, remoteId, params.platform, title]);

  // Same precedence as an existing empty session: project setting, then
  // the last pick in this project, then the directory's most recent model.
  const activeModel = catalog?.projectDefaultModel || getProjectModel(directory) || catalog?.defaultModel || '';
  const seeded = useRef<string>('');
  useEffect(() => {
    if (!catalog || !activeModel || seeded.current === directory) return;
    seeded.current = directory;
    setSelectedModel(activeModel);
  }, [activeModel, directory, catalog]);

  const models = useMemo(() => {
    const entries = catalog?.models.models ?? [];
    return Array.from(new Set([activeModel, ...entries.map((m) => formatModelRef(m.provider, m.model))].filter(Boolean)));
  }, [activeModel, catalog]);
  const agents = useMemo(() => catalog?.agents ?? [], [catalog]);

  const handleModelChange = useCallback((model: string) => {
    seeded.current = directory;
    setSelectedModel(model);
    setSelectedReasoning('');
    saveProjectModel(directory, model);
  }, [directory]);
  const handleAgentChange = useCallback((agent: string) => {
    seeded.current = directory;
    setSelectedAgent(agent);
    const agentModel = agentModelRef(agents.find((a) => a.name === agent));
    if (agentModel) { setSelectedModel(agentModel); setSelectedReasoning(''); }
  }, [agents, directory]);
  const handleToggleFavorite = useCallback(async (provider: string, model: string, next: boolean) => {
    if (!platform) return;
    try {
      await (next ? api.addFavorite(platform, provider, model) : api.removeFavorite(platform, provider, model));
      setCatalogAttempt((value) => value + 1);
    } catch (err) {
      remoteLog.error('Failed to toggle favorite', err);
    }
  }, [platform]);

  const canWorktree = worktreesCapable && (eligibility.resolved?.canCreate ?? false);
  const effectiveAgent = selectedAgent || catalog?.defaultAgent || '';

  // Create the session at the target, then either the server has sent the
  // prompt or the client runs `execute` on the new session. Failures stay
  // retryable on that session, independently of the next composer draft.
  const start = useCallback(async (
    text: string,
    send: StartSessionRequest['send'] | undefined,
    execute?: (sessionId: string, platform: string) => Promise<void>,
  ) => {
    if (!catalog) throw new Error('Session catalog is still loading');
    const sourceGeneration = generation.current;
    if (inFlight.current === sourceGeneration) throw new Error('Session creation is already in progress');
    const stillCurrent = () => active.current && generation.current === sourceGeneration;
    inFlight.current = sourceGeneration;
    setError('');
    // The first submission launches the instance when it is closed
    // (10-20 s); the card above the composer reports it (quick starts stay silent).
    launchProgressReporter.begin(directory, { skipLaunch: true });
    try {
      const res = await api.startSession({
        directory: target.startsWith('dir:') ? target.slice(4) : directory,
        platform, remoteId, title, prompt: text, send,
        worktree: canWorktree && target === 'worktree',
      });
      launchProgressReporter.succeed();
      if (!res.sessionId) throw new Error('Session creation returned no session');
      // No title: OpenCode titles the session from its first message.
      seedNewSession(res.sessionId, res.directory, res.platform, title, res.remoteId);
      if (send && !res.firstMessageSent) {
        recordFailedSend(res.sessionId, {
          id: crypto.randomUUID(), text, images: send.images, model: send.model, agent: send.agent, reasoning: send.reasoning,
          error: res.firstMessageError || 'First message was not sent.', failedAt: Date.now(),
        });
      }
      if (execute) {
        startFirstSubmission(res.sessionId, text, () => execute(res.sessionId, res.platform));
      }
      if (stillCurrent()) {
        navigateToSession(res.sessionId);
        // Only the initiating draft may be cleared, never a newer route's draft.
        clearDraft(NEW_SESSION_ID);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      launchProgressReporter.fail(message);
      if (stillCurrent()) setError(message);
      // Creation is non-idempotent: a lost response must never enter the
      // existing Composer's BackendUnavailableError automatic replay loop.
      throw new Error(message);
    } finally {
      if (inFlight.current === sourceGeneration) inFlight.current = undefined;
    }
  }, [directory, remoteId, platform, title, canWorktree, target, seedNewSession, navigateToSession, catalog]);

  const onSend = (text: string, images?: AttachedImage[], _queue?: boolean, files?: File[]) => {
    const send = { message: text, images, model: selectedModel, agent: effectiveAgent || undefined, reasoning: selectedReasoning || undefined };
    return files?.length ? start(text, undefined, sendFirstFiles(send, files)) : start(text, send);
  };

  const onCommand = (command: string, args: string) => {
    if (command === 'wt' || command === 'worktree') {
      openWorktreeForm({ projectDir: projectRootForDirectory(directory), branch: args.trim() || undefined, remoteId });
      return;
    }
    if (BUILTIN_COMMANDS.some((entry) => entry.name === command)) {
      setError(`/${command} needs an existing conversation.`);
      return;
    }
    return start(`/${command}${args ? ` ${args}` : ''}`, undefined, (id, sessionPlatform) =>
      postJSON<void>(`/api/session/${encodeURIComponent(id)}/command?platform=${encodeURIComponent(sessionPlatform)}`,
        { command, arguments: args, model: selectedModel, agent: effectiveAgent, reasoning: selectedReasoning }, { parseJSON: false }));
  };

  const onShell = (command: string) => start(`!${command}`, undefined, (id, sessionPlatform) =>
    postJSON<void>(`/api/session/${encodeURIComponent(id)}/shell?platform=${encodeURIComponent(sessionPlatform)}`,
      { command, agent: effectiveAgent }, { parseJSON: false }));

  // Switching machines only re-points the route; the shared draft survives.
  const onMachineChange = async (machine: TargetCandidate) => {
    navigate(newSessionPath({ directory: machine.dir, remoteId: machine.remoteId, platform: machine.platform, title }));
  };

  const resolving = !eligibility.resolved || !catalog;
  return (
    // Same shell as AssistantThread: an empty viewport pushes the composer
    // to the bottom with the thread's padding.
    <div className="oc-thread" data-testid="new-conversation">
      <div className="oc-thread-viewport" />
      <div className="oc-viewport-footer" data-testid="conversation-composer">
        {eligibility.error && <InlineAlert onRetry={eligibility.retry}>{eligibility.error}</InlineAlert>}
        {catalogError && <InlineAlert onRetry={() => setCatalogAttempt((value) => value + 1)}>{catalogError}</InlineAlert>}
        {error && <InlineAlert>{error}</InlineAlert>}
        <LaunchProgressCard directory={directory} />
        <Composer
          key={`${remoteId}:${directory}:${params.platform}:${title}`}
          composerRef={composerRef}
          onSend={onSend}
          onCommand={onCommand}
          onShell={caps.shellExec ? onShell : undefined}
          shellExec={caps.shellExec}
          isRunning={false}
          disabled={resolving}
          disabledHint={!catalog ? 'Preparing session…' : resolving ? 'Checking session target…' : undefined}
          whisperAvailable={whisperAvailable}
          models={models}
          modelEntries={catalog?.models.models ?? []}
          selectedModel={selectedModel}
          onModelChange={handleModelChange}
          onToggleFavorite={handleToggleFavorite}
          onRefreshModels={() => setCatalogAttempt((value) => value + 1)}
          activeAgent={catalog?.defaultAgent}
          selectedAgent={selectedAgent}
          onAgentChange={handleAgentChange}
          agents={agents}
          agentsLoaded={!!catalog}
          commands={catalog?.commands}
          selectedReasoning={selectedReasoning}
          onReasoningChange={setSelectedReasoning}
          directory={directory}
          platform={platform}
          newConversation
          worktreesSupported={canWorktree}
          worktrees={eligibility.resolved?.worktrees}
          target={canWorktree ? target : 'current'}
          onTargetChange={setTarget}
          remoteId={remoteId}
          onMachineChange={onMachineChange}
          draftKey={NEW_SESSION_ID}
        />
      </div>
    </div>
  );
}
