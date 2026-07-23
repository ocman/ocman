import { useEffect, useRef, useState } from 'react';
import type { FormEvent } from 'react';
import { Link } from 'react-router-dom';
import { api, type PromptSchedule } from '../lib/api';
import './PromptSchedules.css';

export function PromptSchedules({ directory, remoteId = 'local' }: { directory: string; remoteId?: string }) {
  const [schedules, setSchedules] = useState<PromptSchedule[]>([]);
  const [prompt, setPrompt] = useState('');
  const [runAt, setRunAt] = useState('');
  const [timingType, setTimingType] = useState<'once' | 'interval' | 'cron'>('once');
  const [intervalMinutes, setIntervalMinutes] = useState('60');
  const [cron, setCron] = useState('0 9 * * *');
  const [timezone, setTimezone] = useState(() => Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC');
  const [sessionMode, setSessionMode] = useState<'fresh' | 'reuse'>('fresh');
  const [busy, setBusy] = useState('');
  const [error, setError] = useState('');
  const listVersion = useRef(0);

  useEffect(() => {
    const controller = new AbortController();
    setSchedules([]);
    setError('');
    const load = () => {
      const version = ++listVersion.current;
      return api.promptSchedules.list(directory, remoteId, controller.signal).then((next) => {
        if (version === listVersion.current) {
          setSchedules(next);
          setError('');
        }
      }).catch((err: Error) => {
        if (err.name !== 'AbortError' && version === listVersion.current) setError(err.message);
      });
    };
    void load();
    const interval = window.setInterval(load, 5000);
    return () => {
      controller.abort();
      window.clearInterval(interval);
    };
  }, [directory, remoteId]);

  const replace = (schedule: PromptSchedule) => {
    setSchedules((current) => current.map((item) => item.id === schedule.id ? schedule : item));
  };

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    setBusy('create');
    setError('');
    try {
      const schedule = await api.promptSchedules.create({
        directory, remoteId, prompt, timingType, timezone, sessionMode,
        ...(timingType === 'once' ? { runAt: Date.parse(runAt) } : {}),
        ...(timingType === 'interval' ? { intervalMinutes: Number(intervalMinutes) } : {}),
        ...(timingType === 'cron' ? { cron } : {}),
      });
      listVersion.current++;
      setSchedules((current) => [schedule, ...current]);
      setPrompt('');
      setRunAt('');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Could not schedule prompt');
    } finally {
      setBusy('');
    }
  };

  const act = async (schedule: PromptSchedule, action: 'cancel' | 'run') => {
    setBusy(schedule.id);
    setError('');
    try {
      replace(action === 'cancel'
        ? await api.promptSchedules.cancel(schedule.id)
        : await api.promptSchedules.runNow(schedule.id));
      listVersion.current++;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Schedule action failed');
    } finally {
      setBusy('');
    }
  };

  const toggleEnabled = async (schedule: PromptSchedule) => {
    setBusy(schedule.id);
    setError('');
    try {
      replace(await api.promptSchedules.setEnabled(schedule.id, !schedule.enabled));
      listVersion.current++;
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Schedule action failed');
    } finally {
      setBusy('');
    }
  };

  return (
    <section className="prompt-schedules" aria-labelledby="prompt-schedules-title">
      <h3 id="prompt-schedules-title">Scheduled prompts</h3>
      <form className="prompt-schedule-form" onSubmit={submit}>
        <label>
          Scheduled prompt
          <textarea required value={prompt} onChange={(event) => setPrompt(event.target.value)} />
        </label>
        <label>
          Timing
          <select value={timingType} onChange={(event) => setTimingType(event.target.value as typeof timingType)}>
            <option value="once">Once</option>
            <option value="interval">Interval</option>
            <option value="cron">Cron</option>
          </select>
        </label>
        {timingType === 'once' && <label>Run at<input type="datetime-local" required value={runAt} onChange={(event) => setRunAt(event.target.value)} /></label>}
        {timingType === 'interval' && <label>Every (minutes)<input type="number" min="1" required value={intervalMinutes} onChange={(event) => setIntervalMinutes(event.target.value)} /></label>}
        {timingType === 'cron' && <label>Cron<input required value={cron} onChange={(event) => setCron(event.target.value)} /></label>}
        <label>Timezone<input required value={timezone} onChange={(event) => setTimezone(event.target.value)} /></label>
        <label>
          Session mode
          <select value={sessionMode} onChange={(event) => setSessionMode(event.target.value as typeof sessionMode)}>
            <option value="fresh">Fresh session</option>
            <option value="reuse">Reuse session</option>
          </select>
        </label>
        <button className="oc-time-range-btn" disabled={busy === 'create'}>Schedule prompt</button>
      </form>
      {error && <p className="prompt-schedule-error" role="alert">{error}</p>}
      <div className="prompt-schedule-list">
        {schedules.map((schedule) => (
          <article key={schedule.id} className="prompt-schedule-item">
            <div className="prompt-schedule-meta">
              <strong>{schedule.state}</strong>
              <span>{schedule.timingType} / {schedule.sessionMode} / {schedule.timezone}</span>
              <time dateTime={new Date(schedule.runAt).toISOString()}>Next: {new Date(schedule.runAt).toLocaleString(undefined, { timeZone: schedule.timezone })}</time>
            </div>
            <pre>{schedule.prompt}</pre>
            {schedule.error && <p className="prompt-schedule-error" role="alert">{schedule.error}</p>}
            <div className="prompt-schedule-actions">
              {schedule.state === 'scheduled' && <>
                <button className="oc-time-range-btn" disabled={busy === schedule.id} onClick={() => act(schedule, 'run')}>Run now</button>
                <button className="oc-time-range-btn" disabled={busy === schedule.id} onClick={() => act(schedule, 'cancel')}>Cancel</button>
              </>}
              {schedule.timingType !== 'once' && schedule.state !== 'running' && <button className="oc-time-range-btn" disabled={busy === schedule.id} onClick={() => toggleEnabled(schedule)}>{schedule.enabled ? 'Disable' : 'Enable'}</button>}
              {schedule.sessionId && <Link to={`/session/${encodeURIComponent(schedule.sessionId)}?platform=${encodeURIComponent(schedule.platform ?? '')}`}>Open session</Link>}
            </div>
          </article>
        ))}
        {schedules.length === 0 && !error && <p>No scheduled prompts.</p>}
      </div>
    </section>
  );
}
