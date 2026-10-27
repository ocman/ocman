import { useEffect, useId, useRef, useState, type CSSProperties } from 'react';
import { api } from '../../lib/api';
import type { BackgroundTask } from '../../lib/sessionBackgroundTasks';
import { Button } from '../../components/Control';
import { Popover } from '../../components/Popover';
import styles from './BackgroundTasksNotice.module.css';

interface Props {
  count: number; tasks?: BackgroundTask[]; sessionId?: string; platform?: string; canStop?: boolean; canClassify?: boolean;
}

export function BackgroundTasksNotice(props: Props) {
  return props.count > 0 ? <RunningBackgroundTasksNotice {...props} /> : null;
}

// Matches the .oc-popover width.
const PANEL_WIDTH = 340;

function RunningBackgroundTasksNotice({ count, tasks = [], sessionId = '', platform = '', canStop = false, canClassify = false }: Props) {
  const trigger = useRef<HTMLButtonElement>(null);
  const id = useId();
  const [position, setPosition] = useState<CSSProperties | null>(null);
  const [pending, setPending] = useState<Set<string>>(new Set());
  const [error, setError] = useState('');
  const [lifecycles, setLifecycles] = useState<Record<string, 'task' | 'service'> | null>(null);
  const open = position !== null;
  useEffect(() => {
    if (!open || !canClassify) return;
    const controller = new AbortController();
    void api.sessionBackgroundTasks(sessionId, platform, controller.signal).then(snapshot => {
      if (!controller.signal.aborted) setLifecycles(Object.fromEntries((snapshot.tasks ?? []).map(task => [task.id, task.lifecycle === 'service' ? 'service' : 'task'])));
    }).catch(err => {
      if (!controller.signal.aborted) setError(`Could not read task types: ${err instanceof Error ? err.message : 'Unknown error'}`);
    });
    return () => controller.abort();
  }, [open, canClassify, sessionId, platform]);
  const close = () => setPosition(null);
  const show = () => {
    const rect = trigger.current?.getBoundingClientRect();
    if (!rect) return;
    const width = Math.min(PANEL_WIDTH, window.innerWidth - 24);
    const left = Math.max(12, Math.min(rect.right - width, window.innerWidth - width - 12));
    setLifecycles(null);
    setError('');
    setPosition({ left, bottom: window.innerHeight - rect.top + 4 });
  };
  const stop = async (task: BackgroundTask) => {
    setPending(previous => new Set(previous).add(task.id));
    setError('');
    try {
      await api.stopBackgroundTask(sessionId, task.id, platform);
    } catch (err) {
      setError(`Could not stop ${task.description || task.id}: ${err instanceof Error ? err.message : 'Unknown error'}`);
    } finally {
      setPending(previous => { const next = new Set(previous); next.delete(task.id); return next; });
    }
  };
  const agentsOnly = tasks.length === count && tasks.every(task => task.kind === 'agent');
  const classify = async (task: BackgroundTask, lifecycle: 'task' | 'service') => {
    setPending(previous => new Set(previous).add(task.id));
    setError('');
    try {
      await api.setBackgroundLifecycle(sessionId, task.id, platform, lifecycle);
      setLifecycles(previous => ({ ...previous, [task.id]: lifecycle }));
    } catch (err) {
      setError(`Could not classify ${task.description || task.id}: ${err instanceof Error ? err.message : 'Unknown error'}`);
    } finally {
      setPending(previous => { const next = new Set(previous); next.delete(task.id); return next; });
    }
  };
  const noun = agentsOnly ? 'agent' : 'task';
  const label = `${count} background ${noun}${count === 1 ? '' : 's'} running`;
  return (
    <span className={styles.root} role="status">
      <Button ref={trigger} variant="ghost" size="compact" className={styles.trigger} title={label} aria-label={label}
        aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined}
        onClick={() => { if (open) close(); else show(); }}
        onKeyDown={event => {
          if (event.key === 'Tab' && !event.shiftKey && open) { event.preventDefault(); document.getElementById(id)?.focus(); }
        }}>
        <i className="bi bi-list-task" aria-hidden="true" />
        <span>{count}</span>
      </Button>
      <Popover open={open} onClose={close} triggerRef={trigger} id={id} label="Background tasks" style={position ?? undefined} autoFocus={false}>
        <div className={styles.content}>
          <h2 className={styles.title}>Background tasks</h2>
          {tasks.length === 0 ? <p>Task details are unavailable from this backend.</p> : <ul className={styles.tasks}>
            {tasks.map(task => <li key={task.id} className={styles.task}>
              <span className={styles.description} title={task.description || task.id}>{task.description || task.id}</span>
              {canClassify && <Button variant="ghost" size="small" disabled={lifecycles === null || pending.has(task.id)}
                title="Tasks delay archive resurfacing until they finish. Persistent services do not."
                aria-label={`Mark ${task.description || task.id} as ${(lifecycles?.[task.id] ?? task.lifecycle) === 'service' ? 'task' : 'service'}`}
                onClick={() => void classify(task, (lifecycles?.[task.id] ?? task.lifecycle) === 'service' ? 'task' : 'service')}>
                {(lifecycles?.[task.id] ?? task.lifecycle) === 'service' ? 'Service' : 'Task'}</Button>}
              {task.kind !== 'agent' && <Button variant="danger" size="small" disabled={!canStop || pending.has(task.id)}
                aria-label={`Stop ${task.description || task.id}`} onClick={() => void stop(task)}>{pending.has(task.id) ? 'Stopping…' : 'Stop'}</Button>}
            </li>)}
          </ul>}
          {tasks.length > 0 && !canStop && <p>Individual stopping is unavailable from this backend or connection.</p>}
          {error && <p role="alert">{error}</p>}
        </div>
      </Popover>
    </span>
  );
}
