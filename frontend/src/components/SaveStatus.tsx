import { type SaveState } from '../lib/useSaveStatus';

/** SaveStatus renders the inline spinner / checkmark / error indicator. */
export function SaveStatus({ state }: { state: SaveState }) {
  if (state === 'idle') return null;
  return (
    <span className="oc-save-status" data-state={state} aria-live="polite">
      {state === 'saving' && (
        <span className="oc-spinner oc-save-status-spinner" data-testid="save-status-spinner" aria-label="Saving" />
      )}
      {state === 'saved' && (
        <span className="oc-save-status-check" data-testid="save-status-saved" aria-label="Saved" title="Saved">&#10003;</span>
      )}
      {state === 'error' && (
        <span className="oc-save-status-error" data-testid="save-status-error" aria-label="Save failed" title="Save failed">&#10007;</span>
      )}
    </span>
  );
}
