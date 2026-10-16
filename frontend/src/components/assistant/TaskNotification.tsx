import type { parseTaskNotification } from '../../lib/taskNotification';
import { MarkdownContent } from './MarkdownText';

export function TaskDetails({ details }: { details: string[][] }) {
  if (!details.length) return null;
  return <details className="oc-task-activity oc-task-details">
    <summary>Task details</summary>
    <dl>{details.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
  </details>;
}

export function TaskNotification({ notification }: { notification: NonNullable<ReturnType<typeof parseTaskNotification>> }) {
  const { summary, status, result, details } = notification;
  const statusClass = status === 'completed' ? 'oc-tool-done'
    : status === 'failed' || status === 'error' ? 'oc-tool-error'
    : status === 'running' ? 'oc-tool-running' : '';
  return (
    <section className={`oc-tool oc-tool-task oc-task-notification ${statusClass}`} aria-label="Task notification">
      <div className="oc-tool-header">
        <span className="oc-tool-label">{summary}</span>
        {status && <span className={`oc-task-status ${statusClass}`}>{status.charAt(0).toUpperCase() + status.slice(1)}</span>}
      </div>
      {result && <div className="oc-task-result oc-task-result-expanded oc-md"><MarkdownContent text={result} /></div>}
      <TaskDetails details={details} />
    </section>
  );
}
