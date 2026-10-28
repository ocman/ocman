import { useCallback, useEffect, useRef } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { useConfirm } from './useConfirm';

const discard = {
  title: 'Discard unsaved changes?',
  description: 'Your edits on this page have not been saved.',
  confirmLabel: 'Discard changes',
  cancelLabel: 'Keep editing',
  destructive: true,
};

function sameOriginTarget(event: MouseEvent): { anchor: HTMLAnchorElement; url: URL } | null {
  if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return null;
  const anchor = (event.target as Element | null)?.closest?.('a[href]');
  if (!(anchor instanceof HTMLAnchorElement) || anchor.hasAttribute('download') || (anchor.target && anchor.target !== '_self')) return null;
  const url = new URL(anchor.href, window.location.href);
  return url.origin === window.location.origin ? { anchor, url } : null;
}

/**
 * Warns before discarding unsaved edits. The app uses a BrowserRouter, where
 * useBlocker is unavailable, so this intercepts in-app link clicks (including
 * main navigation) and page unloads. Browser Back is not intercepted.
 * `confirmDiscard` lets an editor's own Cancel or Close ask the same question.
 */
export function useUnsavedChangesGuard(dirty: boolean) {
  const confirm = useConfirm();
  const navigate = useNavigate();
  const location = useLocation();
  const current = location.pathname + location.search + location.hash;
  // Set while replaying an approved click, and once leaving is approved.
  const approved = useRef(false);

  useEffect(() => {
    if (!dirty) return;
    approved.current = false;
    const onBeforeUnload = (event: BeforeUnloadEvent) => { if (!approved.current) event.preventDefault(); };
    const onClick = (event: MouseEvent) => {
      if (approved.current) return;
      const target = sameOriginTarget(event);
      if (!target || target.url.pathname + target.url.search + target.url.hash === current) return;
      event.preventDefault();
      event.stopPropagation();
      void confirm(discard).then((ok) => {
        if (!ok) return;
        approved.current = true;
        // Replay the original click so the link keeps its own behavior,
        // such as replace navigation and onClick callbacks.
        if (target.anchor.isConnected) target.anchor.click();
        else navigate(target.url.pathname + target.url.search + target.url.hash);
      });
    };
    window.addEventListener('beforeunload', onBeforeUnload);
    // Capture runs before the router's own link handler.
    document.addEventListener('click', onClick, true);
    return () => {
      window.removeEventListener('beforeunload', onBeforeUnload);
      document.removeEventListener('click', onClick, true);
    };
  }, [dirty, current, confirm, navigate]);

  const confirmDiscard = useCallback(() => dirty ? confirm(discard) : Promise.resolve(true), [dirty, confirm]);
  return { confirmDiscard };
}
