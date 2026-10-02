import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { api, fetchJSON, postJSON, type GitInfo, type Session, type WorktreeCreateResponse } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import type { ComposerProps } from './composerTypes';
import type { SessionTarget } from './ComposerSelectorRow';
import { InlineAlert } from '../InlineAlert';

export function WorktreeStart({ children, ...props }: ComposerProps & {
  children: (props: ComposerProps) => ReactNode;
}) {
  const navigate = useNavigate();
  const { id: routeSessionId } = useParams();
  const [target, setTarget] = useState<SessionTarget>('worktree');
  const [resolved, setResolved] = useState<{ session: Session; repo: boolean }>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const created = useRef<WorktreeCreateResponse | undefined>(undefined);
  const inFlight = useRef(false);
  const { sessionId, directory, newConversation } = props;

  useEffect(() => {
    if (!newConversation) return;
    const controller = new AbortController();
    void (async () => {
      try {
        const detail = await api.session(sessionId!, undefined, undefined, controller.signal);
        const session = detail.session;
        const query = new URLSearchParams({ dirs: directory!, remoteId: session.remoteId || 'local' });
        const info = await fetchJSON<Record<string, GitInfo>>(`/api/git/info?${query}`, controller.signal);
        if (!controller.signal.aborted) setResolved({ session, repo: !!info[directory!] });
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
      }
    })();
    return () => controller.abort();
  }, [sessionId, directory, newConversation, attempt]);

  const onSend: ComposerProps['onSend'] = async (text, images, queue) => {
    if (!newConversation) return props.onSend?.(text, images, queue);
    if (routeSessionId !== undefined && routeSessionId !== sessionId) {
      throw new Error('Session changed; wait for the current conversation to load');
    }
    if (!resolved) throw new Error('Session target is still loading');
    if (!resolved.repo || target === 'current') return props.onSend?.(text, images, queue);
    if (inFlight.current) return;
    inFlight.current = true;
    setError('');
    const { session } = resolved;
    try {
      if (!created.current) {
        // Creation is not retried automatically after an uncertain transport failure.
        try {
          created.current = await postJSON<WorktreeCreateResponse>(
            `/api/worktree/create-and-launch?platform=${encodeURIComponent(session.platform)}`,
            { projectDir: directory, autoName: true, prompt: text, parentSessionId: sessionId, remoteId: session.remoteId || 'local' },
          );
        } catch (err) {
          throw new Error(err instanceof Error ? err.message : String(err));
        }
      }
      const child = created.current;
      if (!child.sessionId) throw new Error('Worktree creation returned no session');
      await api.sendMessage(child.sessionId, text, images, props.selectedModel,
        props.selectedAgent || props.activeAgent, props.selectedReasoning, session.platform, queue);
      useApiStore.getState().seedNewSession(child.sessionId, child.worktreePath, session.platform, child.branch, session.remoteId || 'local');
      navigate(`/session/${encodeURIComponent(child.sessionId)}`);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      throw err;
    } finally {
      inFlight.current = false;
    }
  };

  return <>
    {error && <InlineAlert onRetry={!resolved ? () => { setError(''); setAttempt((value) => value + 1); } : undefined}>{error}</InlineAlert>}
    {children({ ...props, onSend, target, onTargetChange: setTarget,
      worktreesSupported: resolved?.repo ?? true,
      disabled: props.disabled || (!!newConversation && !resolved),
      disabledHint: newConversation && !resolved ? 'Checking session target…' : props.disabledHint,
      onLaunchRequest: !newConversation || resolved ? props.onLaunchRequest : undefined,
    })}
  </>;
}
