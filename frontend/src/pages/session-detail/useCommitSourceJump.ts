import { useEffect, useRef, useState } from 'react';
import { api } from '../../lib/api';
import type { Message, Part, Session, SessionInfoCommit } from '../../lib/api';
import { parsePart } from '../../lib/convertMessages';
import { remoteLog } from '../../lib/remoteLog';
import { storedMessageIds } from './sessionView';

// Loads the whole transcript, like the message jump picker.
const ALL_MESSAGES_LIMIT = 2_147_483_647;

export interface CommitSourceJump {
  sessionId: string;
  platformId: string;
  messageId: string;
  toolCallId: string;
  tick: number;
}

interface CommitSourceStatus {
  sessionId: string;
  platformId: string;
  message: string;
}

function hasCommitSource(messages: Message[], parts: Part[], commit: SessionInfoCommit): boolean {
  if (!messages.some((message) => message.id === commit.sourceMessageId)) return false;
  const part = parts.find((candidate) => (
    candidate.id === commit.toolPartId && candidate.messageId === commit.sourceMessageId
  ));
  if (!part) return false;
  const data = parsePart(part);
  const callID = data.callID || part.id;
  return data.type === 'tool'
    && (data.tool === 'bash' || data.tool === 'mcp_bash')
    && callID === (commit.toolCallId || commit.toolPartId);
}

/**
 * Jump state for "show the call that made this commit". Declared before
 * useSession so the target message can be protected from trimming; the
 * navigation itself is wired by useCommitSourceJump once the session loads.
 */
export function useCommitSourceState() {
  const [jump, setJump] = useState<CommitSourceJump | null>(null);
  const [status, setStatus] = useState<CommitSourceStatus | null>(null);
  const abortRef = useRef<AbortController | null>(null);
  return { jump, setJump, status, setStatus, abortRef };
}

type CommitSourceState = ReturnType<typeof useCommitSourceState>;

interface CommitSourceView {
  id: string | undefined;
  session: Session | null | undefined;
  messages: Message[];
  parts: Part[];
  hydrateHistory: (messages: Message[], parts: Part[], storedIds: readonly string[]) => void;
  closeMobilePanel: () => void;
}

/**
 * Scrolls to the source call of a commit, loading the full transcript when
 * the call is outside the loaded window. Returns the click handler and the
 * failure message for the current session and owner.
 */
export function useCommitSourceJump(state: CommitSourceState, { id, session, messages, parts, hydrateHistory, closeMobilePanel }: CommitSourceView) {
  const { setJump, status, setStatus, abortRef } = state;

  useEffect(() => () => {
    abortRef.current?.abort();
    abortRef.current = null;
  }, [abortRef, id, session?.platform]);

  useEffect(() => {
    // A route/owner change invalidates navigation state from the previous transcript.
    setJump(null);
    setStatus(null);
  }, [setJump, setStatus, id, session?.platform]);

  const showCommitSource = (commit: SessionInfoCommit) => {
    if (!session || session.id !== id) return;
    abortRef.current?.abort();
    abortRef.current = null;
    const sessionId = session.id;
    const platformId = session.platform;
    setStatus(null);
    const jump = () => {
      closeMobilePanel();
      setJump((current) => ({
        sessionId,
        platformId,
        messageId: commit.sourceMessageId,
        toolCallId: commit.toolCallId || commit.toolPartId,
        tick: (current?.tick ?? 0) + 1,
      }));
    };

    if (hasCommitSource(messages, parts, commit)) {
      jump();
      return;
    }

    const controller = new AbortController();
    abortRef.current = controller;
    void api.session(sessionId, ALL_MESSAGES_LIMIT, 0, controller.signal, platformId)
      .then((detail) => {
        if (controller.signal.aborted) return;
        if (detail.session.id !== sessionId || detail.session.platform !== platformId) return;
        if (!hasCommitSource(detail.messages, detail.parts, commit)) {
          setStatus({ sessionId, platformId, message: 'Source call is no longer available.' });
          return;
        }
        jump();
        hydrateHistory(detail.messages, detail.parts, storedMessageIds(detail));
      })
      .catch((error) => {
        if (controller.signal.aborted) return;
        remoteLog.error('Failed to load commit source', error);
        setStatus({ sessionId, platformId, message: 'Source call could not be loaded.' });
      });
  };

  const commitSourceStatus = status && status.sessionId === session?.id && status.platformId === session?.platform ? status.message : null;
  return { showCommitSource, commitSourceStatus };
}
