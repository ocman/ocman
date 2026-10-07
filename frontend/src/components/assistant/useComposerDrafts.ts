import { useCallback, useLayoutEffect, useRef, type RefObject } from 'react';
import { discardDraft, getDraft, getDraftVersion, saveDraft, clearDraft } from '../../lib/composerDraft';

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
  const versionRef = useRef(0);

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
    // Scheduling is a fresh user edit, unlike an already scheduled callback.
    const version = versionRef.current = getDraftVersion(sid);
    timerRef.current = setTimeout(() => {
      if (version !== getDraftVersion(sid)) return;
      const text = getText().trim();
      if (text) saveDraft(sid, text, version);
      else { discardDraft(sid); versionRef.current = getDraftVersion(sid); }
      persistedRef.current = text;
    }, 300);
  }, [cancelPending]);

  // Load the session's draft; flush the text under the same session when it
  // changes or the composer unmounts (before the next load overwrites it).
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el || !sessionId) return;
    versionRef.current = getDraftVersion(sessionId);
    el.value = persistedRef.current = getDraft(sessionId);
    return () => {
      cancelPending();
      const currentVersion = versionRef.current;
      if (currentVersion !== getDraftVersion(sessionId)) return;
      const text = el.value.trim();
      if (text === persistedRef.current) return;
      // eslint-disable-next-line react-hooks/exhaustive-deps -- the live in-flight prompt is wanted here
      if (text && text === inFlightRef.current) return;
      if (text) saveDraft(sessionId, text, currentVersion);
      else discardDraft(sessionId);
    };
  }, [sessionId, inputRef, inFlightRef, cancelPending]);

  return { clearDraftNow, scheduleDraftSave };
}
