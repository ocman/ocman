import { useCallback, useLayoutEffect, useRef, type RefObject } from 'react';
import { discardDraft, getDraft, getDraftVersion, saveDraft, clearDraft, subscribeDraftTexts } from '../../lib/composerDraft';

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
  /** Presentation only (bash mode, slash menu) for text synchronized from storage; never saves. */
  onSync?: (text: string) => void,
) {
  const onSyncRef = useRef(onSync);
  // Latest callback, published at commit; it only runs from storage subscriptions.
  useLayoutEffect(() => { onSyncRef.current = onSync; });
  const timerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  // What this hook last loaded or persisted for the current key. An unchanged
  // textarea must not overwrite storage someone else updated meanwhile (a
  // failed send restoring its prompt, another tab).
  const persistedRef = useRef('');
  const versionRef = useRef(0);
  const ownSaveRef = useRef<{ sid: string; text: string; revision: number } | undefined>(undefined);

  const cancelPending = useCallback(() => {
    if (timerRef.current) {
      clearTimeout(timerRef.current);
      timerRef.current = null;
    }
  }, []);

  /** Clear this composer's autosave or submitted text, retaining independent peer edits. */
  const clearDraftNow = useCallback((sid: string, submittedText?: string, submittedRevision?: number) => {
    cancelPending();
    const own = ownSaveRef.current;
    const expected = submittedText !== undefined && own?.sid === sid && own.revision === submittedRevision ? own.text : submittedText;
    ownSaveRef.current = undefined;
    clearDraft(sid, expected, submittedRevision);
    persistedRef.current = getDraft(sid);
  }, [cancelPending]);

  const syncDraftNow = useCallback((sid: string) => {
    const el = inputRef.current;
    if (!el || sid !== sessionId) return;
    ownSaveRef.current = undefined;
    versionRef.current = getDraftVersion(sid);
    el.value = persistedRef.current = getDraft(sid);
    onSyncRef.current?.(el.value);
  }, [inputRef, sessionId]);

  /** Debounced autosave (300ms). Empty text clears the draft instead. */
  const scheduleDraftSave = useCallback((sid: string, getText: () => string) => {
    cancelPending();
    // Emptying is applied at the gesture: a delayed clear must not invalidate
    // edits other composers make to this draft after it.
    if (!getText()) {
      discardDraft(sid);
      versionRef.current = getDraftVersion(sid);
      persistedRef.current = '';
      return;
    }
    // Scheduling is a fresh user edit, unlike an already scheduled callback.
    const version = versionRef.current = getDraftVersion(sid);
    timerRef.current = setTimeout(() => {
      timerRef.current = null;
      if (version !== getDraftVersion(sid)) return;
      const text = getText();
      if (text) {
        saveDraft(sid, text, version);
        ownSaveRef.current = { sid, text, revision: version };
      }
      else { discardDraft(sid); versionRef.current = getDraftVersion(sid); }
      persistedRef.current = text;
    }, 300);
  }, [cancelPending]);

  // Load the session's draft; flush the text under the same session when it
  // changes or the composer unmounts (before the next load overwrites it).
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el || !sessionId) return;
    ownSaveRef.current = undefined;
    versionRef.current = getDraftVersion(sessionId);
    el.value = persistedRef.current = getDraft(sessionId);
    return () => {
      cancelPending();
      const currentVersion = versionRef.current;
      if (currentVersion !== getDraftVersion(sessionId)) return;
      const text = el.value;
      if (text === persistedRef.current) return;
      // eslint-disable-next-line react-hooks/exhaustive-deps -- the live in-flight prompt is wanted here
      if (text && text.trim() === inFlightRef.current) return;
      if (text) saveDraft(sessionId, text, currentVersion);
      else discardDraft(sessionId);
    };
  }, [sessionId, inputRef, inFlightRef, cancelPending]);

  // An untouched composer (showing exactly what it last loaded or saved)
  // mirrors stored text changed elsewhere: another tab's edit, its send, or a
  // failed start restored by another tab. A local edit always wins.
  useLayoutEffect(() => {
    const el = inputRef.current;
    if (!el || !sessionId) return;
    return subscribeDraftTexts(() => {
      const stored = getDraft(sessionId);
      if (stored === persistedRef.current || el.value !== persistedRef.current || timerRef.current || inFlightRef.current) return;
      // Synchronizing is not a user edit: no autosave, no revision change.
      syncDraftNow(sessionId);
    });
  }, [sessionId, inputRef, inFlightRef, syncDraftNow]);

  return { clearDraftNow, scheduleDraftSave, syncDraftNow };
}
