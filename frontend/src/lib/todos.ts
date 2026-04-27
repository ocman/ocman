// Shared todo-list parsing used by the assistant thread (where every
// todowrite tool call is rendered inline) and the right-hand "Session
// info" panel (which surfaces the latest list as a live snapshot of
// the conversation's outstanding work).
//
// Pure data: no JSX. The matching <TodoList /> component lives in
// components/TodoList.tsx so the JSX import doesn't pull this module
// into the React-component graph (and fast-refresh rules don't get
// upset).

export interface TodoItem {
  content: string;
  status: string;
  priority: string;
}

// parseTodos pulls a TodoItem[] out of an OpenCode / Claude Code
// `todowrite` tool call. The tool's input arrives in two shapes
// across versions and platforms:
//
//   1. argsText is a JSON object  -> { todos: [...] }
//   2. argsText is a JSON array directly
//   3. argsText is some surrounding text with a JSON array embedded
//      somewhere in it (older OpenCode TUI prefixed an args summary)
//
// The `result` fallback handles the case where the tool just echoes
// back the same payload as its output. Returns null when no
// recognisable todo list is found, so callers can choose between
// rendering a list or hiding the section entirely.
export function parseTodos(argsText: string, result: unknown): TodoItem[] | null {
  const sources = [argsText, typeof result === 'string' ? result : JSON.stringify(result)];
  for (const src of sources) {
    if (!src) continue;
    try {
      const parsed = JSON.parse(src);
      const todos = parsed?.todos || parsed;
      if (Array.isArray(todos) && todos.length > 0 && todos[0]?.content && todos[0]?.status) {
        return todos as TodoItem[];
      }
    } catch {
      // Try to find JSON within the string (may have prefix lines)
      const jsonStart = src.indexOf('[');
      const jsonEnd = src.lastIndexOf(']');
      if (jsonStart >= 0 && jsonEnd > jsonStart) {
        try {
          const todos = JSON.parse(src.slice(jsonStart, jsonEnd + 1));
          if (Array.isArray(todos) && todos.length > 0 && todos[0]?.content && todos[0]?.status) {
            return todos as TodoItem[];
          }
        } catch { /* not JSON */ }
      }
    }
  }
  return null;
}
