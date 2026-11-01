import type { parsePRWatchNotification } from '../../lib/prWatchNotification';

export function PRWatchNotification({ notification }: {
  notification: NonNullable<ReturnType<typeof parsePRWatchNotification>>;
}) {
  return <section className="oc-tool oc-tool-task oc-task-notification" aria-label="PR watch notification">
    {notification.notices.map((notice, index) => <div key={index}>
      <div className="oc-tool-header">
        <i className="bi bi-git" aria-hidden="true" />
        <span className="oc-tool-label">{notice.stopped ? 'PR watch complete' : 'PR watch update'}</span>
        <a href={notice.url} target="_blank" rel="noopener noreferrer">PR #{notice.number}</a>
      </div>
      <div className="oc-task-result oc-task-result-expanded" style={{ whiteSpace: 'pre-wrap' }}>{notice.body}</div>
    </div>)}
    <details className="oc-task-activity oc-task-details">
      <summary>Automation context</summary>
      <p>{notification.guard}</p>
    </details>
  </section>;
}
