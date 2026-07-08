import { useCallback, useEffect, useRef, type RefObject } from 'react';
import { getDraft, saveDraft, clearDraft } from '../../lib/composerDraft';

/**
 * Owns per-session composer draft persistence: loading the saved draft
 * into the textarea when the session changes, debounced autosave while
 * typing, and a final save on unmount. Extracted from Composer to keep
 * the draft timer + its four call sites in one place.
 *
 * The returned helpers replace the repeated inline
 * `clearTimeout(timer); clearDraft(sid)` / `setTimeout(saveDraft)` blocks.
 */
export function useComposerDrafts(
  inputRef: RefObject<HTMLTextAreaElement | null>,
  sessionId: string | undefined,
  sessionIdRef: RefObject<string | undefined>,
) {
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

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
  }, [cancelPending]);

  /** Debounced autosave (300ms). Empty text clears the draft instead. */
  const scheduleDraftSave = useCallback((sid: string, getText: () => string) => {
    cancelPending();
    timerRef.current = setTimeout(() => {
      const text = getText().trim();
      if (text) saveDraft(sid, text);
      else clearDraft(sid);
    }, 300);
  }, [cancelPending]);

  // Load the saved draft into the textarea whenever the session changes.
  useEffect(() => {
    const el = inputRef.current;
    if (!el || !sessionId) return;
    const draft = getDraft(sessionId);
    el.value = draft;
    el.style.height = 'auto';
    if (draft) el.style.height = Math.min(el.scrollHeight, 200) + 'px';
    el.focus();
  }, [sessionId, inputRef]);

  // Flush the current text to storage on unmount.
  useEffect(() => {
    const el = inputRef.current;
    const sidRef = sessionIdRef;
    return () => {
      cancelPending();
      const sid = sidRef.current;
      if (el && sid) {
        const text = el.value.trim();
        if (text) saveDraft(sid, text);
        else clearDraft(sid);
      }
    };
  }, [inputRef, sessionIdRef, cancelPending]);

  return { clearDraftNow, scheduleDraftSave };
}
