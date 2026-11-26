import { useEffect, useState } from 'react';
import { settingAnchor, type SettingId } from './settingsCatalog';

/** One jump to a setting. A new object per pick, so picking it again re-runs. */
export type RevealRequest = { id: SettingId; seq: number };

/**
 * Focus, scroll to and briefly highlight a setting's row once it renders. Rows that
 * wait on one or more fetches appear some time after their section mounts, so
 * watch the DOM until the row shows up, the request changes, or the page
 * unmounts. Returns whether the current request's row has been found.
 */
export function useRevealSetting(request: RevealRequest | null): boolean {
  const [found, setFound] = useState<RevealRequest | null>(null);
  useEffect(() => {
    if (!request) return;
    let clearHighlight: (() => void) | undefined;
    const observer = new MutationObserver(() => { reveal(); });
    function reveal() {
      const row = document.getElementById(settingAnchor(request!.id));
      if (!row) return false;
      observer.disconnect();
      focusSetting(row);
      const reducedMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
      row.scrollIntoView?.({ block: 'center', behavior: reducedMotion ? 'instant' : 'smooth' });
      row.classList.add('settings-row--found');
      const clear = setTimeout(() => row.classList.remove('settings-row--found'), 2000);
      clearHighlight = () => {
        clearTimeout(clear);
        row.classList.remove('settings-row--found');
      };
      setFound(request);
      return true;
    }
    if (!reveal()) observer.observe(document.body, { childList: true, subtree: true });
    return () => { observer.disconnect(); clearHighlight?.(); };
  }, [request]);
  return request !== null && found === request;
}

function focusSetting(row: HTMLElement) {
  // Skip the Example summary: it explains the setting but isn't its control.
  const controls = row.querySelectorAll<HTMLElement>('input:not([type="hidden"]), select, textarea, button, a[href]');
  for (const control of controls) {
    if (control.closest('[aria-disabled="true"], [aria-hidden="true"]')) continue;
    control.focus({ preventScroll: true });
    if (document.activeElement === control) return;
  }
  row.focus({ preventScroll: true });
}
