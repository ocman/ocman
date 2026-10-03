// The new-conversation composer. There is no session yet: the first
// submission creates one at the selected machine + target (worktree or
// current checkout) and sends the prompt in the same request, so the id,
// directory and machine never change under the user afterwards.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, postJSON, type PrepareSessionResponse, type StartSessionRequest } from '../../lib/api';
import type { TargetCandidate } from '../../lib/api.types';
import { useApiStore } from '../../lib/apiStore';
import { BUILTIN_COMMANDS } from '../../lib/commands/builtinCommands';
import { clearDraft, saveDraft } from '../../lib/composerDraft';
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
import { useWorktreeEligibility } from './useWorktreeEligibility';

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
  const [catalogAttempt, setCatalogAttempt] = useState(0);
  const platform = catalog?.platform || params.platform;
  const caps = usePlatformCapabilities(platform);

  // Prepare boots the project's instance when it is closed (10-20 s), so
  // the launch overlay reports it; the catalog fills in when it lands.
  useEffect(() => {
    const controller = new AbortController();
    setCatalog(undefined);
    launchProgressReporter.begin(directory, { skipLaunch: true });
    api.prepareSession({ directory, platform: params.platform }, controller.signal).then((result) => {
      if (controller.signal.aborted) return;
      setCatalog(result);
      launchProgressReporter.succeed();
    }).catch((err) => {
      if (controller.signal.aborted) return;
      launchProgressReporter.fail(err instanceof Error ? err.message : String(err));
    });
    return () => controller.abort();
  }, [directory, params.platform, catalogAttempt]);

  const [selectedModel, setSelectedModel] = useState('');
  const [selectedAgent, setSelectedAgent] = useState('');
  const [selectedReasoning, setSelectedReasoning] = useState('');
  const [target, setTarget] = useState<SessionTarget>('worktree');
  const [error, setError] = useState('');
  const inFlight = useRef(false);

  // Same precedence as an existing empty session: project setting, then
  // the last pick in this project, then the directory's most recent model.
  const activeModel = catalog?.projectDefaultModel || getProjectModel(directory) || catalog?.defaultModel || '';
  const seeded = useRef<string>('');
  useEffect(() => {
    if (!activeModel || seeded.current === directory) return;
    seeded.current = directory;
    setSelectedModel(activeModel);
  }, [activeModel, directory]);

  const models = useMemo(() => {
    const entries = catalog?.models.models ?? [];
    return Array.from(new Set([activeModel, ...entries.map((m) => formatModelRef(m.provider, m.model))].filter(Boolean)));
  }, [activeModel, catalog]);
  const agents = useMemo(() => catalog?.agents ?? [], [catalog]);

  const handleModelChange = useCallback((model: string) => {
    setSelectedModel(model);
    setSelectedReasoning('');
    saveProjectModel(directory, model);
  }, [directory]);
  const handleAgentChange = useCallback((agent: string) => {
    setSelectedAgent(agent);
    const agentModel = agentModelRef(agents.find((a) => a.name === agent));
    if (agentModel) { setSelectedModel(agentModel); setSelectedReasoning(''); }
  }, [agents]);
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
  // prompt or the client runs `execute` on the new session. A send failure
  // keeps the text as the new session's draft so nothing is lost.
  const start = useCallback(async (
    text: string,
    send: StartSessionRequest['send'] | undefined,
    execute?: (sessionId: string, platform: string) => Promise<void>,
  ) => {
    if (inFlight.current) return;
    inFlight.current = true;
    setError('');
    try {
      const res = await api.startSession({
        directory: target.startsWith('dir:') ? target.slice(4) : directory,
        platform: params.platform, title, prompt: text, send,
        worktree: canWorktree && target === 'worktree',
      });
      if (!res.sessionId) throw new Error('Session creation returned no session');
      // No title: OpenCode titles the session from its first message.
      seedNewSession(res.sessionId, res.directory, res.platform, title, res.remoteId);
      if (send && !res.firstMessageSent) {
        remoteLog.error('First message was not sent', res.firstMessageError);
        saveDraft(res.sessionId, text);
      }
      navigateToSession(res.sessionId);
      // The composer unmounted during navigation and flushed its text as
      // the shared new-conversation draft; that text now belongs to the session.
      clearDraft(NEW_SESSION_ID);
      if (execute) {
        execute(res.sessionId, res.platform).catch((err) => {
          remoteLog.error('First submission failed', err);
          saveDraft(res.sessionId, text);
        });
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      throw err;
    } finally {
      inFlight.current = false;
    }
  }, [directory, params.platform, title, canWorktree, target, seedNewSession, navigateToSession]);

  const onSend = (text: string, images?: AttachedImage[]) => start(text, {
    message: text, images, model: selectedModel, agent: effectiveAgent || undefined, reasoning: selectedReasoning || undefined,
  });

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

  // Switching machines only re-points the route: the composer stays
  // mounted, so the draft and selections survive.
  const onMachineChange = async (machine: TargetCandidate) => {
    navigate(newSessionPath({ directory: machine.dir, remoteId: machine.remoteId, platform: machine.platform, title }));
  };

  const resolving = !eligibility.resolved;
  return (
    <div className="oc-new-conversation" data-testid="new-conversation">
      {eligibility.error && <InlineAlert onRetry={eligibility.retry}>{eligibility.error}</InlineAlert>}
      {error && <InlineAlert>{error}</InlineAlert>}
      <Composer
        composerRef={composerRef}
        onSend={onSend}
        onCommand={onCommand}
        onShell={caps.shellExec ? onShell : undefined}
        shellExec={caps.shellExec}
        isRunning={false}
        disabled={resolving}
        disabledHint={resolving ? 'Checking session target…' : undefined}
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
  );
}
