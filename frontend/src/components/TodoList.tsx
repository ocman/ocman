import type { TodoItem } from '../lib/todos';

// TodoList renders a parsed todo list as a checklist with a status
// glyph per row (✓ for completed, ▶ for in-progress, ○ for pending)
// and an optional "!" badge for high-priority items. CSS classes
// (.oc-todo-*) are defined in AssistantThread.css; consumers are
// expected to load that stylesheet (every page that embeds the
// assistant thread does, and the right-panel page is one of them).
//
// The data parsing helper lives in `lib/todos.ts` so this file
// remains pure JSX — keeps fast-refresh and the
// react-refresh/only-export-components lint rule happy.
export function TodoList({ todos }: { todos: TodoItem[] }) {
  return (
    <div className="oc-todo-list">
      {todos.map((t, i) => {
        const isDone = t.status === 'completed';
        const isActive = t.status === 'in_progress';
        let cls = 'oc-todo-item';
        if (isDone) cls += ' oc-todo-done';
        if (isActive) cls += ' oc-todo-active';
        return (
          <div key={i} className={cls}>
            <span
              className="oc-todo-check"
              title={isDone ? 'Completed' : isActive ? 'In progress' : 'Pending'}
            >
              {isDone ? '\u2713' : isActive ? '\u25B6' : '\u25CB'}
            </span>
            <span className="oc-todo-text">{t.content}</span>
            {t.priority === 'high' && (
              <span className="oc-todo-priority" title="High priority">!</span>
            )}
          </div>
        );
      })}
    </div>
  );
}
