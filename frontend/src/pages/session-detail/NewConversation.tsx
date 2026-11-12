import { useCallback, useEffect, useImperativeHandle, useMemo, useRef, useState } from 'react';
import { api, postJSON } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { BUILTIN_COMMANDS } from '../../lib/commands/builtinCommands';
import { beginConversationStart, completeConversationStart, endConversationStart, failConversationStart, forgetConversationDraft, getConversationDraft, retryConversationDraftSave, routeKeyOf, selectionsKey, useNewConversationDrafts } from '../../lib/newConversationDrafts';
import { recordFailedSend } from '../../lib/failedSends';
import { NEW_SESSION_ID, newSessionPath, type NewSessionParams } from '../../lib/newSessionPath';
import { getProjectModel, saveProjectModel } from '../../lib/projectModel';
import { saveSessionModel } from '../../lib/sessionModel';
import { useSettingsRevision } from '../../lib/projectSettingsCache';
import { remoteLog } from '../../lib/remoteLog';
import { agentModelRef, formatModelRef } from '../../lib/sessionStatus';
import { useUiStore } from '../../lib/uiStore';
import { useWorktreeLaunch, usePlatformCapabilities } from '../../lib/useCapabilities';
import { basePlatform, qualifyModelRef } from '../../lib/modelRef';
import { useConnectionSelection } from './useConnectionSelection';
import { onSessionStartProgress } from '../../lib/useGlobalEvents';
import { projectRootForDirectory } from '../../lib/worktrees';
import { randomId } from '../../lib/randomId';
import { draftDefaultModel } from './draftCatalog';
import { useDraftCatalog } from './useDraftCatalog';
import { Composer, type ComposerHandle } from '../../components/assistant/Composer';
import type { AttachedImage } from '../../components/assistant/useComposerAttachments';
import { useConversationSelections } from './useConversationSelections';
import { InlineAlert } from '../../components/InlineAlert';
import { useWorktreeEligibility } from './useWorktreeEligibility';
import { startFirstSubmission } from './firstSubmission';
import { sendFirstFiles } from './sendFirstFiles';
import { StartProgress, type StartSteps } from './StartProgress';
import { startHandoffs, startModels } from './startHandoffs';
import { PreparedDraftLifecycle } from './PreparedDraftLifecycle';
import { navigateAfterDraftDiscard } from './navigateAfterDraftDiscard';
import { useDraftMachine } from './useDraftMachine';
import { usePendingDraftPayload, pendingAttachmentWriter, rememberPendingDraftPayload, updateDraftAttachments } from '../../lib/pendingDraftPayloads';
import { durableStartRequest, resolveTarget, type Ready, type Submission } from './newConversationSubmission';
import { useDurableConversation } from './useDurableConversation';
import { archiveBackendDraft, discardBackendDraft, isBackendDraft, shouldArchiveBackendDraft, useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import { DurableDraftRecovery } from './DurableDraftRecovery';
import { DraftSaveError } from './DraftSaveError';
import { DraftPermissionMode } from '../../components/PermissionModeLock';
import { PERMISSION_MODES } from '../../lib/permissionModes';

export interface NewConversationProps {
  params: NewSessionParams;
  composerRef: React.Ref<ComposerHandle>;
  navigate: (path: string, options?: { replace?: boolean }) => void;
  navigateToSession: (id: string) => void;
}

export function NewConversation({ params, composerRef, navigate, navigateToSession }: NewConversationProps) {
  const recovered = useBackendConversationDrafts((state) => state.recovered[params.draftId ?? ''] ?? 0);
  if (isBackendDraft(params.draftId)) return <DurableDraftRecovery key={params.draftId} params={params}><PreparedConversation key={recovered} params={params} composerRef={composerRef} navigate={navigate} navigateToSession={navigateToSession} /></DurableDraftRecovery>;
  return <PreparedDraftLifecycle key={params.draftId || NEW_SESSION_ID} params={params} navigate={navigate} navigateToSession={navigateToSession}>
    <PreparedConversation key={params.draftId || NEW_SESSION_ID} params={params}
      composerRef={composerRef} navigate={navigate} navigateToSession={navigateToSession} />
  </PreparedDraftLifecycle>;
}

function PreparedConversation({ params, composerRef, navigate }: NewConversationProps) {
  const localComposer = useRef<ComposerHandle>(null);
  useImperativeHandle(composerRef, () => ({ openModelPicker: (query) => localComposer.current?.openModelPicker(query), openAgentPicker: (query) => localComposer.current?.openAgentPicker(query) }), []);
  const { directory, title } = params;
  const draftId = params.draftId || NEW_SESSION_ID;
  const durable = useDurableConversation(params, navigate);
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
  const remoteId = params.remoteId || 'local';
  // Snapshot the destination's selections on navigation, not on each autosave.
  const saved = useMemo(() => {
    const draft = getConversationDraft(draftId);
    return draft?.directory === directory && (draft.remoteId || 'local') === remoteId ? draft : undefined;
  }, [draftId, remoteId, directory]);
  const savedSelections = saved && (saved.remoteId || 'local') === remoteId ? saved : undefined;
  const seedNewSession = useApiStore((state) => state.seedNewSession);
  const openWorktreeForm = useUiStore((state) => state.openWorktreeForm);
  const worktreesCapable = useWorktreeLaunch(remoteId);
  const eligibility = useWorktreeEligibility(directory, remoteId, !!savedSelections?.clone);
  const catalogKey = JSON.stringify([remoteId, directory, params.platform]);
  const [catalogAttempt, setCatalogAttempt] = useState(0);
  const settingsRevision = useSettingsRevision();
  const catalogRequest = JSON.stringify([catalogKey, catalogAttempt, settingsRevision]);
  const { catalog, defaultCatalog, catalogReady, catalogError } = useDraftCatalog({ directory, remoteId, platform: params.platform }, catalogKey, catalogRequest);
  const platform = catalog?.platform || params.platform;
  const caps = usePlatformCapabilities(platform);
  const { selectedModel, setSelectedModel, selectedAgent, setSelectedAgent, selectedReasoning, setSelectedReasoning,
    targetPick, setTarget, permissionMode, setPermissionMode, selectConnection } = useConversationSelections(params, savedSelections, navigate);
  const target = targetPick || catalog?.defaults?.worktree || 'worktree';
  const [error, setError] = useState('');
  // The submitted prompt, shown as the conversation's first message while
  // the session starts. Keyed by route so a machine switch mid-start hides it.
  const routeKey = routeKeyOf({ remoteId, directory, platform: params.platform, title });
  const [pending, setPending] = useState<{ key: string; text: string; images?: AttachedImage[]; startId: string; steps: StartSteps }>();
  const inFlight = useRef<number | undefined>(undefined);
  const { cloneURL, machineSource, onMachineChange, saveDraft } = useDraftMachine(params,
    { model: selectedModel, agent: selectedAgent, reasoning: selectedReasoning, target: targetPick, permissionMode: permissionMode === 'default' ? undefined : permissionMode }, () => inFlight.current !== undefined, navigate);
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
  const activeModel = draftDefaultModel(defaultCatalog, getProjectModel(directory));
  const attachmentPayload = usePendingDraftPayload(draftId);
  const models = useMemo(() => {
    const entries = catalog?.models.models ?? [];
    return Array.from(new Set([activeModel, ...entries.map((m) => qualifyModelRef(m.platform, formatModelRef(m.provider, m.model)))].filter(Boolean)));
  }, [activeModel, catalog]);
  const agents = useMemo(() => catalog?.agents ?? [], [catalog]);

  const { platformForModel, modelPicker, named: namedConnections } = useConnectionSelection(remoteId, platform, selectConnection, durable.submitted,
    { ready: !!catalog, error: catalogError, retry: () => setCatalogAttempt((value) => value + 1) });
  // Re-points the route when model runs on another platform; reports whether it did.
  const repoint = useCallback((model: string) => {
    if (durable.submitted) return false;
    const next = platformForModel(model);
    if (!next || !platform || basePlatform(next) === basePlatform(platform)) return false;
    navigate(newSessionPath({ directory, remoteId, platform: next, title, draftId }));
    return true;
  }, [directory, remoteId, title, draftId, platform, platformForModel, navigate, durable.submitted]);
  // A default model of another platform (the project's or the last pick)
  // opens the conversation on that platform.
  useEffect(() => {
    if (catalogReady && !selectedModel && activeModel) repoint(activeModel);
  }, [catalogReady, selectedModel, activeModel, repoint]);
  const handleModelChange = useCallback((model: string) => {
    if (namedConnections && !platformForModel(model)) { setError('Choose a connection for this model before changing platforms.'); return; }
    if (durable.submitted && platformForModel(model) && platformForModel(model) !== durable.record?.request?.platform) { setError('The platform cannot change after the first prompt is submitted.'); return; }
    setSelectedModel(model);
    setSelectedReasoning('');
    saveProjectModel(directory, model);
    if (repoint(model)) setSelectedAgent(''); // agents belong to a platform
  }, [directory, repoint, durable.submitted, durable.record?.request?.platform, platformForModel, namedConnections, setSelectedModel, setSelectedAgent, setSelectedReasoning]);
  const handleAgentChange = useCallback((agent: string) => {
    setSelectedAgent(agent);
    const agentModel = agentModelRef(agents.find((a) => a.name === agent));
    if (agentModel) { setSelectedModel(agentModel); setSelectedReasoning(''); }
  }, [agents, setSelectedAgent, setSelectedModel, setSelectedReasoning]);
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
  }, [platform, platformForModel, setCatalogAttempt]);

  // 'default' and platforms without rule support keep the owner/project defaults.
  const permissionRules = caps.permissionRules && permissionMode !== 'default' ? PERMISSION_MODES.find((m) => m.id === permissionMode)?.rules : undefined;

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

  const start = useCallback(async (text: string, build: (ready: Ready) => Submission, images?: AttachedImage[]) => {
    if (durable.enabled) {
      const r = await waitReady();
      const submission = build(r);
      if (submission.send) {
        try {
          await durable.send(durableStartRequest(r, submission, { directory, remoteId, title, target: targetPick, platformForModel, cloneURL, permissionRules }, text), submission.files);
          if (submission.model) saveSessionModel(draftId, submission.model);
          setError('');
          return;
        } catch (error) { const message = error instanceof Error ? error.message : String(error); setError(message); throw new Error(message); }
      }
    }
    const sourceGeneration = generation.current;
    if (inFlight.current === sourceGeneration) throw new Error('Session creation is already in progress');
    let revision: number | null = null;
    const stillCurrent = () => active.current && generation.current === sourceGeneration;
    inFlight.current = sourceGeneration;
    setError('');
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
      // Back can restore this route before the machine-switch render commits.
      // Persist the submitted route even when its autosave effect did not rerun.
      saveDraft();
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
      const submitted = { revision, routeKey, selections: selectionsKey({ model: selectedModel, agent: selectedAgent, reasoning: selectedReasoning, target: targetPick, permissionMode: permissionMode === 'default' ? undefined : permissionMode }) };
      const startTarget = resolveTarget(targetPick || ready.catalog.defaults?.worktree || 'worktree', ready.canWorktree, ready.worktrees);
      const res = await api.startSession({
        directory: startTarget.startsWith('dir:') ? startTarget.slice(4) : directory,
        // The model picks the platform; a pick racing its route change still lands there.
        platform: (model && platformForModel(model)) || ready.platform, remoteId, title, prompt: text, send, startId,
        worktree: startTarget === 'worktree', cloneUrl: cloneURL, ...(permissionRules ? { permissionRules } : {}),
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
  }, [directory, remoteId, title, routeKey, targetPick, selectedModel, selectedAgent, selectedReasoning, permissionMode, permissionRules, seedNewSession, draftId, platformForModel, cloneURL, saveDraft, durable]);

  const onSend = (text: string, images?: AttachedImage[], _queue?: boolean, files?: File[]) => {
    rememberPendingDraftPayload(draftId, images, files);
    return start(text, (r) => {
      const { model, agent, reasoning } = pick(r);
      const send = { message: text, images, model, agent: agent || undefined, reasoning: reasoning || undefined };
      return files?.length && !durable.enabled ? { model, execute: sendFirstFiles(send, files) } : { model, send, files };
    }, images);
  };

  const onCommand = (command: string, args: string) => {
    if (command === 'archive') {
      const archive = shouldArchiveBackendDraft(durable.record);
      const afterCommit = () => {
        if (active.current) navigateAfterDraftDiscard(draftId, navigate);
      };
      return (archive ? archiveBackendDraft(draftId).then(afterCommit)
        : (durable.enabled ? discardBackendDraft : forgetConversationDraft)(draftId, afterCommit)).catch((error: unknown) => {
        if (active.current) setError(`Could not ${archive ? 'archive the session' : 'discard the draft'}: ${error instanceof Error ? error.message : String(error)}`);
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
    return start(`/${command}${args ? ` ${args}` : ''}`, (r) => ({ model: pick(r).model,
      ...(durable.enabled ? { send: { message: `/${command} ${args}`.trim(), ...pick(r) }, command: { command, arguments: args } } : {}), execute: (id, sessionPlatform) =>
      postJSON<void>(`/api/session/${encodeURIComponent(id)}/command?platform=${encodeURIComponent(sessionPlatform)}`,
        { command, arguments: args, ...pick(r) }, { parseJSON: false }) }));
  };

  const onShell = (command: string) => start(`!${command}`, (r) => ({ model: pick(r).model,
    ...(durable.enabled ? { send: { message: `!${command}`, ...pick(r) }, shell: { command } } : {}), execute: (id, sessionPlatform) =>
    postJSON<void>(`/api/session/${encodeURIComponent(id)}/shell?platform=${encodeURIComponent(sessionPlatform)}`,
      { command, agent: pick(r).agent }, { parseJSON: false }) }));

  return (
    // Same shell as AssistantThread: an empty viewport pushes the composer
    // to the bottom with the thread's padding.
    <div className="oc-thread" data-testid="new-conversation">
      <div className="oc-thread-viewport">
        {durable.submitted && <StartProgress platform={durable.record?.request?.platform} prompt={durable.record?.request?.prompt || ''} images={durable.record?.request?.send?.images} steps={durable.record?.steps ?? {}} timestamp={durable.record?.submittedAt} complete={durable.record?.phase === 'ready'} />}
        {pending?.key === routeKey && <StartProgress platform={platform} prompt={pending.text} images={pending.images} steps={pending.steps} />}
        {pending?.key !== routeKey && draftStart && !draftStart.sessionId && !draftStart.error && <StartProgress platform={platform} prompt={draftStart.text} images={attachmentPayload.images} steps={{}} />}
      </div>
      <div className="oc-viewport-footer" data-testid="conversation-composer">
        {cloneURL && <p role="status">On first submission, clone {cloneURL} into {directory} on the selected machine. Local changes will not transfer.</p>}
        {eligibility.error && <InlineAlert onRetry={eligibility.retry}>{eligibility.error}</InlineAlert>}
        {catalogError && <InlineAlert onRetry={() => setCatalogAttempt((value) => value + 1)}>{catalogError}</InlineAlert>}
        {saveError && <InlineAlert onRetry={() => retryConversationDraftSave(draftId)}>Draft selections not saved: {saveError}</InlineAlert>}
        <DraftSaveError id={draftId} error={durable.saveError} retry={durable.retrySave} navigate={navigate} getLocalText={() => localComposer.current?.getText?.()} />
        {(error || draftStart?.error || durable.record?.error) && <InlineAlert onRetry={durable.record?.error && durable.record.result?.sessionId === draftId ? durable.retry : undefined}>{error || draftStart?.error || durable.record?.error}</InlineAlert>}
        <Composer
          key={recoveryKey}
          composerRef={localComposer}
          initialAttachments={attachmentPayload}
          onAttachmentProcessing={() => pendingAttachmentWriter(draftId)}
          onAttachmentsChange={(payload) => { if (getConversationDraft(draftId)) updateDraftAttachments(draftId, payload); }}
          onSend={onSend}
          onCommand={onCommand}
          onShell={caps.shellExec ? onShell : undefined}
          shellExec={caps.shellExec}
           isRunning={false}
           queuedMessages={durable.queue.queue} onRemoveQueuedMessage={durable.queue.remove} onMoveQueuedMessage={durable.queue.move}
          disabled={!!draftStart && !draftStart.sessionId && !draftStart.error}
          models={models}
          modelEntries={catalog?.models.models ?? []} modelPicker={modelPicker}
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
           newConversation={!durable.submitted}
          worktreesSupported={canWorktree}
          worktrees={eligibility.resolved?.worktrees}
          target={effectiveTarget}
          onTargetChange={setTarget}
          remoteId={remoteId}
           onMachineChange={durable.submitted ? undefined : onMachineChange}
          machineSource={machineSource}
          draftKey={draftId}
          permissionControl={caps.permissionRules && !durable.submitted ? <DraftPermissionMode value={permissionMode} onChange={setPermissionMode} disabled={!!draftStart && !draftStart.sessionId && !draftStart.error} /> : undefined}
        />
      </div>
    </div>
  );
}
