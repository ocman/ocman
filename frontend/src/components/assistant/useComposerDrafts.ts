import { useCallback, useEffect, useRef, type RefObject } from 'react';
import { getDraft, saveDraft, clearDraft } from '../../lib/composerDraft';

/**
 * Owns per-session composer draft persistence: loading the saved draft
 * into the textarea when the session changes, debounced autosave while
 * typing, and a final save when the session changes or the composer
 * unmounts. `inFlightRef` holds the prompt currently being sent: it is
 * never parked as a draft, or it reappears after the send lands.
 */
export function useComposerDrafts(
  inputRef: RefObject<HTMLTextAreaElement | null>,
  sessionId: string | undefined,
  inFlightRef: RefObject<string | null>,
) {
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // What this hook last loaded or persisted for the current key. An unchanged
  // textarea must not overwrite storage someone else updated meanwhile (a
  // failed send restoring its prompt, another tab).
  const persistedRef = useRef('');

  const cancelPending = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  /** Cancel any pending autosave and drop the stored draft for `sid`. */
  const clearDraftNow = useCallback((sid: string) => {
    cancelPending();
    clearDraft(sid);
    persistedRef.current = '';
  }, [cancelPending]);

  /** Debounced autosave (300ms). Empty text clears the draft instead. */
  const scheduleDraftSave = useCallback((sid: string, getText: () => string) => {
    cancelPending();
    timerRef.current = setTimeout(() => {
      const text = getText().trim();
      if (text) saveDraft(sid, text);
      else clearDraft(sid);
      persistedRef.current = text;
    }, 300);
  }, [cancelPending]);

  // Load the session's draft; flush the text under the same session when it
  // changes or the composer unmounts (before the next load overwrites it).
  useEffect(() => {
    const el = inputRef.current;
    if (!el || !sessionId) return;
    el.value = persistedRef.current = getDraft(sessionId);
    return () => {
      cancelPending();
      const text = el.value.trim();
      if (text === persistedRef.current) return;
      // eslint-disable-next-line react-hooks/exhaustive-deps -- the live in-flight prompt is wanted here
      if (text && text === inFlightRef.current) return;
      if (text) saveDraft(sessionId, text);
      else clearDraft(sessionId);
    };
  }, [sessionId, inputRef, inFlightRef, cancelPending]);

  return { clearDraftNow, scheduleDraftSave };
}
