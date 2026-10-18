import { useEffect, useId, useRef, useState, type CSSProperties } from 'react';
import { api } from '../../lib/api';
import type { BackgroundTask } from '../../lib/sessionBackgroundTasks';
import { Button } from '../../components/Control';
import { Popover } from '../../components/Popover';
import { TOOLTIP_DELAY_MS } from '../../components/Tooltip';
import styles from './BackgroundTasksNotice.module.css';

interface Props {
  count: number; tasks?: BackgroundTask[]; sessionId?: string; platform?: string; canStop?: boolean;
}

export function BackgroundTasksNotice(props: Props) {
  return props.count > 0 ? <RunningBackgroundTasksNotice {...props} /> : null;
}

function RunningBackgroundTasksNotice({ count, tasks = [], sessionId = '', platform = '', canStop = false }: Props) {
  const trigger = useRef<HTMLButtonElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const id = useId();
  const [position, setPosition] = useState<CSSProperties | null>(null);
  const [pending, setPending] = useState<Set<string>>(new Set());
  const [error, setError] = useState('');
  const open = count > 0 && position !== null;
  const cancelTimer = () => clearTimeout(timer.current);
  const close = () => { cancelTimer(); setPosition(null); };
  const show = () => {
    cancelTimer();
    const rect = trigger.current?.getBoundingClientRect();
    if (rect) setPosition({ left: Math.max(12, Math.min(rect.left, window.innerWidth - 372)), bottom: window.innerHeight - rect.top + 4 });
  };
  const enter = () => { cancelTimer(); if (!open) timer.current = setTimeout(show, TOOLTIP_DELAY_MS); };
  const leave = () => { cancelTimer(); timer.current = setTimeout(close, 150); };
  useEffect(() => () => clearTimeout(timer.current), []);
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
  const text = `${count} background ${count === 1 ? 'task' : 'tasks'} running`;
  return (
    <div className="oc-sse-indicator" role="status">
      <Button ref={trigger} variant="ghost" size="compact" aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined}
        onMouseEnter={enter} onMouseLeave={leave} onFocus={() => { if (!open) show(); }} onClick={() => { if (!open) show(); }}
        onBlur={event => { if (!document.getElementById(id)?.contains(event.relatedTarget)) close(); }}
        onKeyDown={event => {
          if (event.key === 'Tab' && !event.shiftKey && open) { event.preventDefault(); document.getElementById(id)?.focus(); }
        }}>{text}</Button>
      <Popover open={open} onClose={close} triggerRef={trigger} id={id} label="Background tasks" style={position ?? undefined} autoFocus={false}>
        <div className={styles.content} onMouseEnter={cancelTimer} onMouseLeave={leave}
          onBlur={event => { if (!event.currentTarget.contains(event.relatedTarget) && !trigger.current?.contains(event.relatedTarget)) close(); }}>
          <h2 className={styles.title}>Background tasks</h2>
          {tasks.length === 0 ? <p>Task details are unavailable from this backend.</p> : <ul className={styles.tasks}>
            {tasks.map(task => <li key={task.id} className={styles.task}>
              <span className={styles.description} title={task.description || task.id}>{task.description || task.id}</span>
              <Button variant="danger" size="small" disabled={!canStop || pending.has(task.id)}
                aria-label={`Stop ${task.description || task.id}`} onClick={() => void stop(task)}>{pending.has(task.id) ? 'Stopping…' : 'Stop'}</Button>
            </li>)}
          </ul>}
          {tasks.length > 0 && !canStop && <p>Individual stopping is unavailable from this backend or connection.</p>}
          {error && <p role="alert">{error}</p>}
        </div>
      </Popover>
    </div>
  );
}
