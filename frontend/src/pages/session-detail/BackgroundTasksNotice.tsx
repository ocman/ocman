export function BackgroundTasksNotice({ count }: { count: number }) {
  if (count <= 0) return null;
  const text = `${count} background ${count === 1 ? 'task' : 'tasks'} running`;
  return (
    <div className="oc-sse-indicator" role="status">
      <span title={text}>{text}</span>
    </div>
  );
}
