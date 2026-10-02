import { useEffect, useRef, useState, type ReactNode } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { api, fetchJSON, postJSON, type GitInfo, type Session, type WorktreeCreateResponse, type WorktreeEntry } from '../../lib/api';
import { useApiStore } from '../../lib/apiStore';
import { BUILTIN_COMMANDS } from '../../lib/commands/builtinCommands';
import type { ComposerProps } from './composerTypes';
import type { SessionTarget } from './ComposerSelectorRow';
import { InlineAlert } from '../InlineAlert';
import { clearWorktreeSubmission, startWorktreeSubmission, useWorktreeSubmission } from './worktreeSubmission';

export function WorktreeStart({ children, ...props }: ComposerProps & {
  children: (props: ComposerProps) => ReactNode;
}) {
  const navigate = useNavigate();
  const { id: routeSessionId } = useParams();
  const [target, setTarget] = useState<SessionTarget>('worktree');
  const [resolved, setResolved] = useState<{ session: Session; canCreate: boolean }>();
  const [error, setError] = useState('');
  const [attempt, setAttempt] = useState(0);
  const created = useRef<WorktreeCreateResponse | undefined>(undefined);
  const inFlight = useRef(false);
  const { sessionId, directory, newConversation } = props;
  const submission = useWorktreeSubmission((state) => state.entries[sessionId ?? '']);

  useEffect(() => {
    if (!newConversation) return;
    const controller = new AbortController();
    void (async () => {
      try {
        const detail = await api.session(sessionId!, undefined, undefined, controller.signal);
        const session = detail.session;
        const query = new URLSearchParams({ dirs: directory!, remoteId: session.remoteId || 'local' });
        const info = await fetchJSON<Record<string, GitInfo>>(`/api/git/info?${query}`, controller.signal);
        const repo = !!info[directory!]?.branch;
        let alreadyChosen = false;
        if (repo) {
          const worktreeQuery = new URLSearchParams({ dir: directory!, remoteId: session.remoteId || 'local' });
          const { worktrees } = await fetchJSON<{ worktrees: WorktreeEntry[] }>(`/api/worktree/list?${worktreeQuery}`, controller.signal);
          // Read the owner's actual workspaces, so manual selection survives reloads
          // and also covers worktrees outside ocman's managed directory layout.
          alreadyChosen = worktrees.some((tree) => !tree.main &&
            (directory === tree.path || directory!.startsWith(`${tree.path}/`)));
        }
        if (!controller.signal.aborted) setResolved({ session, canCreate: repo && !alreadyChosen });
      } catch (err) {
        if (!controller.signal.aborted) setError(err instanceof Error ? err.message : String(err));
      }
    })();
    return () => controller.abort();
  }, [sessionId, directory, newConversation, attempt]);

  const dispatch = async (
    text: string,
    current: () => void | Promise<void>,
    execute: (sessionId: string, platform: string) => Promise<void>,
  ) => {
    const runCurrent = () => {
      clearWorktreeSubmission(sessionId!);
      return current();
    };
    if (!newConversation) return runCurrent();
    if (routeSessionId !== undefined && routeSessionId !== sessionId) {
      throw new Error('Session changed; wait for the current conversation to load');
    }
    if (!resolved) throw new Error('Session target is still loading');
    if (!resolved.canCreate || target === 'current') return runCurrent();
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
      useApiStore.getState().seedNewSession(child.sessionId, child.worktreePath, session.platform, child.branch, session.remoteId || 'local');
      navigate(`/session/${encodeURIComponent(child.sessionId)}`);
      startWorktreeSubmission(child.sessionId, text, () => execute(child.sessionId!, session.platform));
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
      throw err;
    } finally {
      inFlight.current = false;
    }
  };

  const onSend: ComposerProps['onSend'] = (text, images, queue) => dispatch(text,
    () => props.onSend?.(text, images, queue),
    (id, platform) => api.sendMessage(id, text, images, props.selectedModel,
      props.selectedAgent || props.activeAgent, props.selectedReasoning, platform, queue));

  const onCommand: ComposerProps['onCommand'] = (command, args) => {
    // Ocman's session/UI actions keep their existing handlers. Custom agent
    // commands execute in the same selected workspace as an ordinary prompt.
    if (command === 'worktree' || BUILTIN_COMMANDS.some((entry) => entry.name === command)) {
      return props.onCommand?.(command, args);
    }
    return dispatch(`/${command}${args ? ` ${args}` : ''}`, () => props.onCommand?.(command, args),
      (id, platform) => postJSON<void>(`/api/session/${encodeURIComponent(id)}/command?platform=${encodeURIComponent(platform)}`,
        { command, arguments: args, model: props.selectedModel, agent: props.selectedAgent || props.activeAgent, reasoning: props.selectedReasoning },
        { parseJSON: false }));
  };

  const onShell: ComposerProps['onShell'] = (command) => dispatch(`!${command}`, () => props.onShell?.(command),
    (id, platform) => postJSON<void>(`/api/session/${encodeURIComponent(id)}/shell?platform=${encodeURIComponent(platform)}`,
      { command, agent: props.selectedAgent || props.activeAgent }, { parseJSON: false }));

  return <>
    {error && <InlineAlert onRetry={!resolved ? () => { setError(''); setAttempt((value) => value + 1); } : undefined}>{error}</InlineAlert>}
    {submission?.error && <InlineAlert onRetry={() => startWorktreeSubmission(sessionId!, submission.text, submission.execute)}>
      {submission.error} First submission: <code>{submission.text}</code>
    </InlineAlert>}
    {children({ ...props, onSend,
      onCommand: props.onCommand ? onCommand : undefined, onShell: props.onShell ? onShell : undefined,
      target: resolved && !resolved.canCreate ? 'current' : target, onTargetChange: setTarget,
      worktreesSupported: resolved?.canCreate ?? true,
      isRunning: props.isRunning || !!submission?.pending,
      disabled: props.disabled || !!submission?.pending || (!!newConversation && !resolved),
      disabledHint: newConversation && !resolved ? 'Checking session target…' : props.disabledHint,
      onLaunchRequest: !newConversation || resolved ? props.onLaunchRequest : undefined,
    })}
  </>;
}
