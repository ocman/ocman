import { useEffect, useState } from 'react';
import { settingAnchor, type SettingId } from './settingsCatalog';

/** One jump to a setting. A new object per pick, so picking it again re-runs. */
export type RevealRequest = { id: SettingId; seq: number };

/**
 * Scroll to and briefly highlight a setting's row once it renders. Rows that
 * wait on one or more fetches appear some time after their section mounts, so
 * watch the DOM until the row shows up, the request changes, or the page
 * unmounts. Returns whether the current request's row has been found.
 */
export function useRevealSetting(request: RevealRequest | null): boolean {
  const [found, setFound] = useState<RevealRequest | null>(null);
  useEffect(() => {
    if (!request) return;
    let clear: ReturnType<typeof setTimeout> | undefined;
    const observer = new MutationObserver(() => { reveal(); });
    function reveal() {
      const row = document.getElementById(settingAnchor(request!.id));
      if (!row) return false;
      observer.disconnect();
      row.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
      row.classList.add('settings-row--found');
      clear = setTimeout(() => row.classList.remove('settings-row--found'), 2000);
      setFound(request);
      return true;
    }
    if (!reveal()) observer.observe(document.body, { childList: true, subtree: true });
    return () => { observer.disconnect(); clearTimeout(clear); };
  }, [request]);
  return request !== null && found === request;
}
