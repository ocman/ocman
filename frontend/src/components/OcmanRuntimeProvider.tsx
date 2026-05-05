import { useCallback, useMemo } from 'react';
import {
  AssistantRuntimeProvider,
  useExternalStoreRuntime,
  type ThreadMessageLike,
} from '@assistant-ui/react';
import type { AgentInfo, Message, Part } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { AgentsContext } from '../lib/agentColor';
import { FailedSendsContext, type FailedSendsContextValue } from '../lib/failedSendsContext';
import type { FailedSend } from '../lib/failedSends';
import { convertMessages, computeIsRunning } from '../lib/convertMessages';

interface Props {
  messages: Message[];
  parts: Part[];
  sessionId: string;
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
  // Live stdout from running task sessions. Maps taskId -> last 10 lines of output.
  taskLiveOutput?: Record<string, string>;
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
  canSend,
  pendingAgent,
  agents,
  taskLiveOutput,
  projectDirectory,
  failedSends,
  onRetryFailedSend,
  onDismissFailedSend,
  children,
}: Props) {
  const agentList = useMemo(() => agents ?? [], [agents]);
  const sendMessage = useApiStore((state) => state.sendMessage);

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

  const converted = useMemo(
    () => convertMessages(messages, parts, pendingAgent, taskLiveOutput, projectDirectory, failedById),
    [messages, parts, pendingAgent, taskLiveOutput, projectDirectory, failedById],
  );

  const isRunning = useMemo(() => computeIsRunning(messages), [messages]);

  // Stable onNew callback — only changes when canSend, sessionId, or
  // sendMessage change. This prevents the store adapter object from
  // getting a new `onNew` reference on every render.
  const onNew = useCallback(async (message: { content: Array<{ type: string; text?: string; image?: string }> }) => {
    if (!canSend) return;
    const textPart = message.content.find((c) => c.type === 'text');
    const text = textPart && textPart.type === 'text' ? textPart.text : '';
    const imageParts = message.content
      .filter((c): c is { type: 'image'; image: string } => c.type === 'image' && 'image' in c)
      .map((c) => ({ url: c.image, mime: 'image/png' }));
    if (!text && imageParts.length === 0) return;
    await sendMessage(sessionId, text, imageParts.length > 0 ? imageParts : undefined);
  }, [canSend, sendMessage, sessionId]);

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
        <AssistantRuntimeProvider runtime={runtime}>
          {children}
        </AssistantRuntimeProvider>
      </FailedSendsContext.Provider>
    </AgentsContext.Provider>
  );
}
