import { useCallback, useEffect, useMemo, useState } from 'react';
import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
  type AppendMessage,
  type ThreadMessageLike,
} from '@assistant-ui/react';
import type { AgentInfo, ChildSessionReference, Message, Part, SessionModelEntry, TaskSessionData } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { useUiStore } from '../lib/uiStore';
import { AgentsContext } from '../lib/agentColor';
import { FailedSendsContext, type FailedSendsContextValue } from '../lib/failedSendsContext';
import type { FailedSend } from '../lib/failedSends';
import { computeIsRunning, createConvertMessages, parsePart } from '../lib/convertMessages';
import { computeTurnStats, ModelLabelsContext, TurnStatsContext } from '../lib/turnStats';
import { formatModelRef } from '../lib/sessionStatus';

interface Props {
  messages: Message[];
  parts: Part[];
  sessionId: string;
  platformId?: string;
  /**
   * Whether the composer may currently send messages. Typically this
   * is `platformCapabilities.composer && portAvailable` — that is, the
   * owning platform supports composition AND the live connection is up.
   * When false, `onNew` is a no-op.
   */
  canSend: boolean;
  // Agent that the user is about to send the next message as. Used to color
  // user messages that haven't been replied to yet.
  pendingAgent?: string;
  // Agent metadata (including colors) loaded from the OpenCode /agent API.
  agents?: AgentInfo[];
  // Model palette metadata used to render friendly model names in turns.
  modelEntries?: SessionModelEntry[];
  // Sub-session data from task sessions for embedded thread rendering.
  taskLiveOutput?: Record<string, TaskSessionData>;
  childSessions?: ChildSessionReference[];
  // Absolute path of the session's working directory. Used to display
  // file paths in muted read lines as project-relative instead of just
  // basenames, so the reader can locate the file in their checkout.
  projectDirectory?: string;
  /** Sends that failed on the client and are awaiting Retry / Dismiss. */
  failedSends?: FailedSend[];
  onRetryFailedSend?: (id: string) => void;
  onDismissFailedSend?: (id: string) => void;
  children: React.ReactNode;
}

export function OcmanRuntimeProvider({
  messages,
  parts,
  sessionId,
  platformId,
  canSend,
  pendingAgent,
  agents,
  modelEntries,
  taskLiveOutput,
  childSessions,
  projectDirectory,
  failedSends,
  onRetryFailedSend,
  onDismissFailedSend,
  children,
}: Props) {
  const agentList = useMemo(() => agents ?? [], [agents]);
  const modelLabels = useMemo(() => {
    const out: Record<string, string> = {};
    for (const entry of modelEntries ?? []) {
      const raw = formatModelRef(entry.provider, entry.model);
      if (raw && entry.modelName) out[raw] = entry.modelName;
    }
    return out;
  }, [modelEntries]);
  const sendMessage = useApiStore((state) => state.sendMessage);
  // Display-only reasoning visibility (the `/thinking` toggle, #290).
  const showReasoning = useUiStore((state) => state.showReasoning);
  const hasActiveReasoning = useMemo(() => showReasoning && parts.some((part) => {
    const data = parsePart(part);
    return data.type === 'reasoning' && data.time?.start !== undefined && data.time.end === undefined;
  }), [parts, showReasoning]);
  const [reasoningNow, setReasoningNow] = useState(Date.now);
  useEffect(() => {
    if (!hasActiveReasoning) return;
    setReasoningNow(Date.now());
    const timer = window.setInterval(() => setReasoningNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [hasActiveReasoning]);

  // Index failed sends by their optimistic message id so convertMessages can
  // attach the failure metadata (and the AssistantThread renderer can pull
  // the entry directly from context).
  const failedById = useMemo(() => {
    const out: Record<string, FailedSend> = {};
    for (const e of failedSends ?? []) out[e.id] = e;
    return out;
  }, [failedSends]);

  const failedCtx = useMemo<FailedSendsContextValue>(() => ({
    byId: failedById,
    retry: onRetryFailedSend ?? (() => {}),
    dismiss: onDismissFailedSend ?? (() => {}),
  }), [failedById, onRetryFailedSend, onDismissFailedSend]);

  // Per-instance converter: each session-detail page mounts its own
  // OcmanRuntimeProvider, so the result-array cache and the
  // `partsByMsg` memo live for the lifetime of THIS session's
  // provider — never cross-contaminating across sessions. The
  // closure is rebuilt on `sessionId` change so a stale cache can't
  // survive a navigation between sessions sharing the same page
  // mount. `sessionId` is in the dep list deliberately even though
  // `createConvertMessages()` doesn't read it — the dep is the
  // *trigger* that drops the previous closure.
  // eslint-disable-next-line react-hooks/exhaustive-deps -- sessionId is the intentional cache-key trigger
  const convert = useMemo(() => createConvertMessages(), [sessionId]);

  const converted = useMemo(() => {
    const serverMessages = convert(messages, parts, pendingAgent, taskLiveOutput, projectDirectory, failedById, showReasoning, childSessions, reasoningNow);
    const serverIds = new Set(serverMessages.map((message) => message.id));
    const missingFailures = (failedSends ?? []).filter((entry) => !serverIds.has(entry.id));
    if (missingFailures.length === 0) return serverMessages;
    return [...serverMessages, ...missingFailures.map((entry): ThreadMessageLike => ({
      id: entry.id,
      role: 'user',
      content: entry.text,
      createdAt: new Date(entry.failedAt),
      metadata: { custom: { failed: { error: entry.error, imagesDropped: !!entry.imagesDropped } } },
    }))];
  }, [convert, messages, parts, pendingAgent, taskLiveOutput, projectDirectory, failedById, failedSends, showReasoning, childSessions, reasoningNow]);

  const isRunning = useMemo(() => computeIsRunning(messages), [messages]);

  const turnStatsMap = useMemo(
    () => computeTurnStats(messages, parts, isRunning),
    [messages, parts, isRunning],
  );

  // Stable onNew callback — only changes when canSend, sessionId, or
  // sendMessage change. This prevents the store adapter object from
  // getting a new `onNew` reference on every render.
  const onNew = useCallback(async (message: AppendMessage) => {
    if (!canSend) return;
    const textPart = message.content.find((c) => c.type === 'text');
    const text = textPart && textPart.type === 'text' ? textPart.text : '';
    const imageParts = message.content
      .filter((c): c is { type: 'image'; image: string } => c.type === 'image' && 'image' in c)
      .map((c) => ({ url: c.image, mime: 'image/png' }));
    if (!text && imageParts.length === 0) return;
    await sendMessage(sessionId, text, imageParts.length > 0 ? imageParts : undefined, undefined, undefined, undefined, platformId);
  }, [canSend, sendMessage, sessionId, platformId]);

  // Memoize the store adapter so useExternalStoreRuntime's
  // unconditional useEffect (`runtime.setAdapter(store)`) receives
  // the same object reference when nothing changed. Without this,
  // every render creates a new adapter object → setAdapter fires →
  // store subscribers re-render → infinite loop.
  const store = useMemo(() => ({
    messages: converted,
    isRunning,
    convertMessage: (m: ThreadMessageLike) => m,
    onNew,
  }), [converted, isRunning, onNew]);

  const runtime = useExternalStoreRuntime(store);

  return (
    <AgentsContext.Provider value={agentList}>
        <FailedSendsContext.Provider value={failedCtx}>
        <ModelLabelsContext.Provider value={modelLabels}>
          <TurnStatsContext.Provider value={turnStatsMap}>
            <AssistantRuntimeProvider runtime={runtime}>
              {children}
            </AssistantRuntimeProvider>
          </TurnStatsContext.Provider>
        </ModelLabelsContext.Provider>
      </FailedSendsContext.Provider>
    </AgentsContext.Provider>
  );
}
