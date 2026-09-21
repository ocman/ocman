import { useNavigate } from 'react-router-dom';
import * as Toast from '@radix-ui/react-toast';
import { useToastNotify, type ToastEntry } from '../lib/useToastNotify';
import { useGlobalEvents } from '../lib/useGlobalEvents';
import { cleanTitle } from '../lib/format';
import './PromptToastNotify.css';

/**
 * Renders an in-app Radix toast for every session that's blocking on
 * user input (pending permission or question). Mounted once at the app
 * root.
 *
 * Each toast has:
 *   - a short descriptor ("question" / "permission")
 *   - the session title and project directory basename
 *   - an "Open session" action that navigates to /session/:id
 *   - a manual close button
 *
 * Toasts persist until dismissed (`duration={Infinity}`); the underlying
 * `useToastNotify` hook clears the entry from state when the user clicks
 * through, and re-emits if the same session prompts again later.
 *
 * Lives inside its own Toast.Provider so it doesn't entangle with the
 * page-local toasts in SessionDetail (which already has its own
 * provider for rename/launch toasts). The viewport is anchored
 * top-right so prompts don't get hidden behind the composer/chat UI.
 */
export function PromptToastNotify() {
  const navigate = useNavigate();
  const { toasts, dismiss } = useToastNotify();
  // Subscribe to the app-wide /api/events stream so a permission that's
  // auto-approved by the LLM judge clears its toast immediately rather
  // than waiting for the next notify poll.
  useGlobalEvents();

  function describe(entry: ToastEntry): { heading: string; body: string } {
    const project = basename(entry.directory);
    // Session titles often come from LLM output and contain markdown
    // decorations (e.g. `**Important** fix`). Strip them so the toast
    // shows plain text, matching every other place we render a title.
    const titlePart = cleanTitle(entry.title) || 'Untitled session';
    const projectPart = project ? ` · ${project}` : '';
    return {
      heading: entry.kind === 'permission'
        ? 'Permission requested'
        : 'Waiting on your answer',
      body: `${titlePart}${projectPart}`,
    };
  }

  function handleOpen(entry: ToastEntry) {
    dismiss(entry.toastId);
    navigate(`/session/${entry.sessionId}`);
  }

  return (
    <Toast.Provider swipeDirection="right" duration={Infinity}>
      {toasts.map((t) => {
        const { heading, body } = describe(t);
        return (
          <Toast.Root
            key={t.toastId}
            className="oc-prompt-toast"
            data-kind={t.kind}
            // Radix infers open from prop; we control it via the array
            // membership so dismiss() removing the entry collapses the
            // root.
            open
            onOpenChange={(open) => {
              if (!open) dismiss(t.toastId);
            }}
            duration={Infinity}
          >
            <Toast.Close asChild>
              <button
                type="button"
                className="oc-prompt-toast-close"
                aria-label="Dismiss"
              >
                ×
              </button>
            </Toast.Close>
            <Toast.Title className="oc-prompt-toast-heading">
              {heading}
            </Toast.Title>
            <Toast.Description className="oc-prompt-toast-body">
              {body}
            </Toast.Description>
            <div className="oc-prompt-toast-actions">
              <Toast.Action
                asChild
                altText="Open session"
                onClick={() => handleOpen(t)}
              >
                <button type="button" className="oc-prompt-toast-open">
                  Open session
                </button>
              </Toast.Action>
            </div>
          </Toast.Root>
        );
      })}
      <Toast.Viewport className="oc-prompt-toast-viewport" />
    </Toast.Provider>
  );
}

/** basename of a directory path; tolerates trailing slashes and empty input. */
function basename(dir: string | undefined): string {
  if (!dir) return '';
  const trimmed = dir.replace(/\/+$/, '');
  const idx = trimmed.lastIndexOf('/');
  return idx === -1 ? trimmed : trimmed.slice(idx + 1);
}
