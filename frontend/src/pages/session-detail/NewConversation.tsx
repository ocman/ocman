// The new-conversation composer. There is no session yet: the first
// submission creates one at the selected machine + target (worktree or
// current checkout) and sends the prompt in the same request, so the id,
// directory and machine never change under the user afterwards.

import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { api, postJSON, type StartSessionRequest } from '../../lib/api';
import type { TargetCandidate } from '../../lib/api.types';
import { useApiStore } from '../../lib/apiStore';
import { BUILTIN_COMMANDS } from '../../lib/commands/builtinCommands';
import { beginConversationStart, completeConversationStart, endConversationStart, failConversationStart, forgetConversationDraft, getConversationDraft, rememberConversationDraft, retryConversationDraftSave, routeKeyOf, selectionsKey, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { shortPath } from '../../lib/format';
import { useHeaderInfo } from '../../lib/headerContext';
import { recordFailedSend } from '../../lib/failedSends';
import { NEW_SESSION_ID, newSessionPath, type NewSessionParams } from '../../lib/newSessionPath';
import { cacheNewSessionCatalog, getNewSessionCatalog } from '../../lib/newSessionCatalogCache';
import { getProjectModel, saveProjectModel } from '../../lib/projectModel';
import { saveSessionModel } from '../../lib/sessionModel';
import { loadProjectSettings, useSettingsRevision } from '../../lib/projectSettingsCache';
import { remoteLog } from '../../lib/remoteLog';
import { agentModelRef, formatModelRef } from '../../lib/sessionStatus';
import { useUiStore } from '../../lib/uiStore';
import { useMachinePlatforms, useOpencodeLaunch, usePlatformCapabilities } from '../../lib/useCapabilities';
import { basePlatform, modelRefPlatform, qualifyModelRef } from '../../lib/modelRef';
import { onSessionStartProgress } from '../../lib/useGlobalEvents';
import { projectRootForDirectory } from '../../lib/worktrees';
import { randomId } from '../../lib/randomId';
import { draftDefaultModel, prepareDraftCatalog, type DraftCatalog } from './draftCatalog';
import { Composer, type ComposerHandle } from '../../components/assistant/Composer';
import type { AttachedImage } from '../../components/assistant/useComposerAttachments';
import type { SessionTarget } from '../../components/assistant/ComposerSelectorRow';
import { InlineAlert } from '../../components/InlineAlert';
import { useWorktreeEligibility } from './useWorktreeEligibility';
import { startFirstSubmission } from './firstSubmission';
import { sendFirstFiles } from './sendFirstFiles';
import { StartProgress, type StartSteps } from './StartProgress';
import { startHandoffs, startModels } from './startHandoffs';
import { PreparedDraftLifecycle } from './PreparedDraftLifecycle';
import { navigateAfterDraftDiscard } from './navigateAfterDraftDiscard';
import { usePendingDraftPayload, pendingAttachmentWriter, rememberPendingDraftPayload, updateDraftAttachments } from '../../lib/pendingDraftPayloads';

export interface NewConversationProps {
  params: NewSessionParams;
  composerRef: React.Ref<ComposerHandle>;
  navigate: (path: string, options?: { replace?: boolean }) => void;
  navigateToSession: (id: string) => void;
}

/** What a submission needs before it can start: the catalog and the resolved target. */
interface Ready { catalog: DraftCatalog; canWorktree: boolean; worktrees: { path: string }[]; platform?: string; model: string }
/** A server-delivered prompt, or work the client runs on the new session. */
interface Submission {
  model: string;
  send?: StartSessionRequest['send'];
  execute?: (sessionId: string, platform: string) => Promise<void>;
}

function resolveTarget(target: SessionTarget, canWorktree: boolean, worktrees: { path: string }[]): SessionTarget {
  return !canWorktree || target.startsWith('dir:') && !worktrees.some((tree) => `dir:${tree.path}` === target) ? 'current' : target;
}

export function NewConversation({ params, composerRef, navigate, navigateToSession }: NewConversationProps) {
  return <PreparedDraftLifecycle key={params.draftId || NEW_SESSION_ID} params={params} navigate={navigate} navigateToSession={navigateToSession}>
    <PreparedConversation key={`${params.draftId || NEW_SESSION_ID}:${params.remoteId || 'local'}:${params.directory}`} params={params}
      composerRef={composerRef} navigate={navigate} navigateToSession={navigateToSession} />
  </PreparedDraftLifecycle>;
}

function PreparedConversation({ params, composerRef, navigate }: NewConversationProps) {
  const { directory, title } = params;
  const draftId = params.draftId || NEW_SESSION_ID;
  const draftStart = useNewConversationDrafts((state) => state.starts[draftId]);
  const saveError = useNewConversationDrafts((state) => state.saveErrors[draftId]);
  const [openedWhilePending] = useState(() => !!draftStart && !draftStart.error && !draftStart.sessionId);
  const recovered = useRef(false);
  const [recoveryKey, setRecoveryKey] = useState(0);
  useEffect(() => {
    if (openedWhilePending && draftStart?.error && !recovered.current) {
      recovered.current = true;
      // Reload recovered text once; later retries must retain attachments.
      setRecoveryKey(1);
    }
  }, [openedWhilePending, draftStart?.error]);
  const [saved] = useState(() => useNewConversationDrafts.getState().drafts.find((draft) => draft.draftId === draftId));
  const remoteId = params.remoteId || 'local';
  const savedSelections = saved?.remoteId === remoteId ? saved : undefined;
  const seedNewSession = useApiStore((state) => state.seedNewSession);
  const openWorktreeForm = useUiStore((state) => state.openWorktreeForm);
  const worktreesCapable = useOpencodeLaunch(remoteId);
  const eligibility = useWorktreeEligibility(directory, remoteId);

  const catalogKey = JSON.stringify([remoteId, directory, params.platform]);
  const cachedCatalog = useMemo(() => getNewSessionCatalog(catalogKey), [catalogKey]);
  const [prepared, setPrepared] = useState<{ key: string; request: string; catalog: DraftCatalog }>();
  // Keyed by request so a new request (route change, retry) clears it without an effect.
  const [catalogFailure, setCatalogFailure] = useState<{ request: string; message: string }>();
  const [catalogAttempt, setCatalogAttempt] = useState(0);
  const settingsRevision = useSettingsRevision();
  const catalogRequest = JSON.stringify([catalogKey, catalogAttempt, settingsRevision]);
  const catalog: DraftCatalog | undefined = prepared?.key === catalogKey ? prepared.catalog : cachedCatalog;
  const catalogReady = prepared?.request === catalogRequest;
  const catalogError = catalogFailure?.request === catalogRequest ? catalogFailure.message : '';
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
    Promise.all([
      api.prepareSession({ directory, remoteId, platform: params.platform }, controller.signal),
      loadProjectSettings(directory, remoteId),
    ]).then(([result, settings]) => {
      if (controller.signal.aborted) return;
      const catalog = prepareDraftCatalog(result, settings);
      cacheNewSessionCatalog(catalogKey, catalog);
      setPrepared({ key: catalogKey, request: catalogRequest, catalog });
    }).catch((err) => {
      if (controller.signal.aborted) return;
      setCatalogFailure({ request: catalogRequest, message: err instanceof Error ? err.message : String(err) });
    });
    return () => controller.abort();
  }, [directory, remoteId, params.platform, catalogKey, catalogRequest]);

  const [selectedModel, setSelectedModel] = useState(savedSelections?.model || '');
  const [selectedAgent, setSelectedAgent] = useState(savedSelections?.agent || '');
  const [selectedReasoning, setSelectedReasoning] = useState(savedSelections?.reasoning || '');
  // A model pick re-points the platform, so selections survive a platform change.
  const machineKey = JSON.stringify([remoteId, directory]);
  const [selectionKey, setSelectionKey] = useState(machineKey);
  if (selectionKey !== machineKey) {
    setSelectionKey(machineKey);
    setSelectedModel(''); setSelectedAgent(''); setSelectedReasoning('');
  }
  const [targetPick, setTarget] = useState<SessionTarget | undefined>(saved?.target as SessionTarget | undefined);
  const target = targetPick || catalog?.defaults?.worktree || 'worktree';
  useEffect(() => {
    rememberConversationDraft({ directory, remoteId, platform: params.platform, title, draftId,
      model: selectedModel, agent: selectedAgent, reasoning: selectedReasoning, target: targetPick });
  }, [directory, remoteId, params.platform, title, draftId, selectedModel, selectedAgent, selectedReasoning, targetPick]);
  const [error, setError] = useState('');
  // The submitted prompt, shown as the conversation's first message while
  // the session starts. Keyed by route so a machine switch mid-start hides it.
  const routeKey = routeKeyOf({ remoteId, directory, platform: params.platform, title });
  const [pending, setPending] = useState<{ key: string; text: string; images?: AttachedImage[]; startId: string; steps: StartSteps }>();
  const inFlight = useRef<number | undefined>(undefined);
  const active = useRef(false);
  const generation = useRef(0);
  const readyWaiters = useRef<{ resolve: (value: Ready) => void; reject: (err: Error) => void }[]>([]);
  useEffect(() => {
    generation.current++;
    active.current = true;
    const waiters = readyWaiters.current;
    return () => {
      active.current = false;
      // A submission still waiting for this route fails, so the composer restores its draft.
      waiters.splice(0).forEach((w) => w.reject(new Error('The session target changed before it was ready')));
    };
  }, [directory, remoteId, params.platform, title]);

  // Same precedence as an existing empty session: project setting, then
  // the last pick in this project, then the directory's most recent model.
  const activeModel = draftDefaultModel(catalog, getProjectModel(directory));
  const attachmentPayload = usePendingDraftPayload(draftId);

  const models = useMemo(() => {
    const entries = catalog?.models.models ?? [];
    return Array.from(new Set([activeModel, ...entries.map((m) => qualifyModelRef(m.platform, formatModelRef(m.provider, m.model)))].filter(Boolean)));
  }, [activeModel, catalog]);
  const agents = useMemo(() => catalog?.agents ?? [], [catalog]);

  // The model's platform is the conversation's: picking another platform's
  // model re-points the route, which loads that platform's agents and commands.
  const machinePlatforms = useMachinePlatforms(remoteId);
  const platformForModel = useCallback((model: string) => {
    const base = modelRefPlatform(model);
    return machinePlatforms.find((p) => basePlatform(p.id) === base)?.id;
  }, [machinePlatforms]);
  // Re-points the route when model runs on another platform; reports whether it did.
  const repoint = useCallback((model: string) => {
    const next = platformForModel(model);
    if (!next || !platform || basePlatform(next) === basePlatform(platform)) return false;
    navigate(newSessionPath({ directory, remoteId, platform: next, title, draftId }));
    return true;
  }, [directory, remoteId, title, draftId, platform, platformForModel, navigate]);
  // A default model of another platform (the project's or the last pick)
  // opens the conversation on that platform.
  useEffect(() => {
    if (catalogReady && !selectedModel && activeModel) repoint(activeModel);
  }, [catalogReady, selectedModel, activeModel, repoint]);
  const handleModelChange = useCallback((model: string) => {
    setSelectedModel(model);
    setSelectedReasoning('');
    saveProjectModel(directory, model);
    if (repoint(model)) setSelectedAgent(''); // agents belong to a platform
  }, [directory, repoint]);
  const handleAgentChange = useCallback((agent: string) => {
    setSelectedAgent(agent);
    const agentModel = agentModelRef(agents.find((a) => a.name === agent));
    if (agentModel) { setSelectedModel(agentModel); setSelectedReasoning(''); }
  }, [agents]);
  // Favorites belong to the platform the model runs on.
  const handleToggleFavorite = useCallback(async (provider: string, model: string, next: boolean, modelPlatform?: string) => {
    const owner = (modelPlatform && platformForModel(`${modelPlatform}:${provider}/${model}`)) || platform;
    if (!owner) return;
    try {
      await (next ? api.addFavorite(owner, provider, model) : api.removeFavorite(owner, provider, model));
      setCatalogAttempt((value) => value + 1);
    } catch (err) {
      remoteLog.error('Failed to toggle favorite', err);
    }
  }, [platform, platformForModel]);

  const canWorktree = worktreesCapable && (eligibility.resolved?.canCreate ?? false);
  const effectiveTarget = resolveTarget(target, canWorktree, eligibility.resolved?.worktrees || []);

  // The composer stays usable while the catalog and target resolve; a
  // submission made before then waits here (or fails with the prepare error).
  const ready: Ready | undefined = eligibility.resolved && catalogReady && catalog ? { catalog, canWorktree, worktrees: eligibility.resolved.worktrees, platform, model: activeModel } : undefined;
  const readyError = catalogError || eligibility.error || '';
  const readyRef = useRef(ready);
  useEffect(() => {
    readyRef.current = ready;
    if (ready) readyWaiters.current.splice(0).forEach((w) => w.resolve(ready));
    else if (readyError) readyWaiters.current.splice(0).forEach((w) => w.reject(new Error(readyError)));
  });
  const waitReady = () => readyRef.current ? Promise.resolve(readyRef.current)
    : readyError ? Promise.reject(new Error(readyError))
    : new Promise<Ready>((resolve, reject) => { readyWaiters.current.push({ resolve, reject }); });
  // Selections are resolved after readiness: a pick made before the catalog
  // arrived wins, otherwise the catalog's defaults apply.
  const pick = (r: Ready) => ({ model: selectedModel || r.model, agent: selectedAgent || r.catalog.defaultAgent || '', reasoning: selectedReasoning });

  // Create the session at the target, then either the server has sent the
  // prompt or the client runs `execute` on the new session. Failures stay
  // retryable on that session, independently of the next composer draft.
  const start = useCallback(async (text: string, build: (ready: Ready) => Submission, images?: AttachedImage[]) => {
    const sourceGeneration = generation.current;
    if (inFlight.current === sourceGeneration) throw new Error('Session creation is already in progress');
    let revision: number | null = null;
    const stillCurrent = () => active.current && generation.current === sourceGeneration;
    inFlight.current = sourceGeneration;
    setError('');
    // The server reports each step (instance, worktree, session, prompt)
    // under the pending prompt; it all goes away with this page.
    const startId = randomId();
    setPending({ key: routeKey, text, images, startId, steps: {} });
    let steps: StartSteps = {};
    // Subscribed before the request so the first step can't be missed.
    const unsubscribe = onSessionStartProgress((id, step, state) => {
      if (id !== startId) return;
      steps = { ...steps, [step]: state };
      setPending((p) => p?.startId === id ? { ...p, steps } : p);
    });
    try {
      revision = await beginConversationStart(draftId, routeKey, text);
      if (revision === null) throw new Error('Session creation is already in progress');
      // Synchronous when ready, so a re-point right after submit cannot drop it.
      let ready = readyRef.current;
      if (!ready) {
        if (!stillCurrent()) throw new Error('The session target changed before it was ready');
        ready = await waitReady();
        if (!stillCurrent()) throw new Error('The session target changed before it was ready');
      }
      const { send, execute, model } = build(ready);
      // Retirement compares these initiating selections, not ones a peer stored meanwhile.
      const submitted = { revision, routeKey, selections: selectionsKey({ model: selectedModel, agent: selectedAgent, reasoning: selectedReasoning, target: targetPick }) };
      const startTarget = resolveTarget(targetPick || ready.catalog.defaults?.worktree || 'worktree', ready.canWorktree, ready.worktrees);
      const res = await api.startSession({
        directory: startTarget.startsWith('dir:') ? startTarget.slice(4) : directory,
        // The model picks the platform; a pick racing its route change still lands there.
        platform: (model && platformForModel(model)) || ready.platform, remoteId, title, prompt: text, send, startId,
        worktree: startTarget === 'worktree',
      });
      if (!res.sessionId) throw new Error('Session creation returned no session');
      if (model) startModels.set(res.sessionId, model);
      if (model) saveSessionModel(res.sessionId, model);
      // No title: OpenCode titles the session from its first message.
      if (send && !res.firstMessageSent) {
        recordFailedSend(res.sessionId, {
          id: randomId(), text, images: send.images, model: send.model, agent: send.agent, reasoning: send.reasoning,
          error: res.firstMessageError || 'First message was not sent.', failedAt: Date.now(),
        });
      }
      if (execute) {
        await startFirstSubmission(res.sessionId, text, () => execute(res.sessionId, res.platform));
      }
      seedNewSession(res.sessionId, res.directory, res.platform, title, res.remoteId);
      if (send && res.firstMessageSent) startHandoffs.set(res.sessionId, { prompt: text, images, steps });
      await completeConversationStart(draftId, { sessionId: res.sessionId, platform: res.platform, remoteId: res.remoteId, directory: res.directory }, submitted, text);
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err);
      // The transaction restores the prompt only if no newer edit or discard happened.
      if (revision !== null) await failConversationStart(draftId, message, { text, revision });
      // Only this request's prompt: a newer start may be pending already.
      setPending((p) => p?.startId === startId ? undefined : p);
      if (stillCurrent()) setError(message);
      // Creation is non-idempotent: a lost response must never enter the
      // existing Composer's BackendUnavailableError automatic replay loop.
      throw new Error(message);
    } finally {
      unsubscribe();
      if (revision !== null) endConversationStart(draftId);
      if (inFlight.current === sourceGeneration) inFlight.current = undefined;
    }
  // waitReady only reads refs.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [directory, remoteId, title, routeKey, targetPick, selectedModel, selectedAgent, selectedReasoning, seedNewSession, draftId, platformForModel]);

  const onSend = (text: string, images?: AttachedImage[], _queue?: boolean, files?: File[]) => {
    rememberPendingDraftPayload(draftId, images, files);
    return start(text, (r) => {
      const { model, agent, reasoning } = pick(r);
      const send = { message: text, images, model, agent: agent || undefined, reasoning: reasoning || undefined };
      return files?.length ? { model, execute: sendFirstFiles(send, files) } : { model, send };
    }, images);
  };

  const onCommand = (command: string, args: string) => {
    if (command === 'archive') {
      return forgetConversationDraft(draftId, () => {
        if (active.current) navigateAfterDraftDiscard(draftId, navigate);
      }).catch((error: unknown) => {
        if (active.current) setError(`Could not discard the draft: ${error instanceof Error ? error.message : String(error)}`);
        throw error;
      });
    }
    if (command === 'wt' || command === 'worktree') {
      openWorktreeForm({ projectDir: projectRootForDirectory(directory), branch: args.trim() || undefined, remoteId });
      return;
    }
    if (BUILTIN_COMMANDS.some((entry) => entry.name === command)) {
      setError(`/${command} needs an existing conversation.`);
      return;
    }
    return start(`/${command}${args ? ` ${args}` : ''}`, (r) => ({ model: pick(r).model, execute: (id, sessionPlatform) =>
      postJSON<void>(`/api/session/${encodeURIComponent(id)}/command?platform=${encodeURIComponent(sessionPlatform)}`,
        { command, arguments: args, ...pick(r) }, { parseJSON: false }) }));
  };

  const onShell = (command: string) => start(`!${command}`, (r) => ({ model: pick(r).model, execute: (id, sessionPlatform) =>
    postJSON<void>(`/api/session/${encodeURIComponent(id)}/shell?platform=${encodeURIComponent(sessionPlatform)}`,
      { command, agent: pick(r).agent }, { parseJSON: false }) }));

  // Switching machines re-points this draft, without touching other drafts.
  const onMachineChange = async (machine: TargetCandidate) => {
    const start = useNewConversationDrafts.getState().starts[draftId];
    if (inFlight.current !== undefined || start && !start.error && !start.sessionId) throw new Error('Session creation is already in progress');
    if (target.startsWith('dir:')) {
      rememberConversationDraft({ ...params, draftId, target: 'current' });
      setTarget('current');
    }
    navigate(newSessionPath({ directory: machine.dir, remoteId: machine.remoteId, platform: machine.platform, title, draftId }));
  };

  return (
    // Same shell as AssistantThread: an empty viewport pushes the composer
    // to the bottom with the thread's padding.
    <div className="oc-thread" data-testid="new-conversation">
      <div className="oc-thread-viewport">
        {pending?.key === routeKey && <StartProgress platform={platform} prompt={pending.text} images={pending.images} steps={pending.steps} />}
        {pending?.key !== routeKey && draftStart && !draftStart.sessionId && !draftStart.error && <StartProgress platform={platform} prompt={draftStart.text} images={attachmentPayload.images} steps={{}} />}
      </div>
      <div className="oc-viewport-footer" data-testid="conversation-composer">
        {eligibility.error && <InlineAlert onRetry={eligibility.retry}>{eligibility.error}</InlineAlert>}
        {catalogError && <InlineAlert onRetry={() => setCatalogAttempt((value) => value + 1)}>{catalogError}</InlineAlert>}
        {saveError && <InlineAlert onRetry={() => retryConversationDraftSave(draftId)}>Draft selections not saved: {saveError}</InlineAlert>}
        {(error || draftStart?.error) && <InlineAlert>{error || draftStart?.error}</InlineAlert>}
        <Composer
          key={`${routeKey}:${recoveryKey}`}
          composerRef={composerRef}
          initialAttachments={attachmentPayload}
          onAttachmentProcessing={() => pendingAttachmentWriter(draftId)}
          onAttachmentsChange={(payload) => { if (getConversationDraft(draftId)) updateDraftAttachments(draftId, payload); }}
          onSend={onSend}
          onCommand={onCommand}
          onShell={caps.shellExec ? onShell : undefined}
          shellExec={caps.shellExec}
          isRunning={false}
          disabled={!!draftStart && !draftStart.sessionId && !draftStart.error}
          models={models}
          modelEntries={catalog?.models.models ?? []}
          selectedModel={selectedModel || activeModel}
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
          target={effectiveTarget}
          onTargetChange={setTarget}
          remoteId={remoteId}
          onMachineChange={onMachineChange}
          draftKey={draftId}
        />
      </div>
    </div>
  );
}
