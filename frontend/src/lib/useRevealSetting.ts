import { useEffect } from 'react';
import { settingAnchor, type SettingId } from './settingsCatalog';

/**
 * Scroll to and briefly highlight a setting's row once it renders. Rows that
 * wait on a fetch appear a moment after their section mounts, so poll for up
 * to a second rather than giving up on the first frame.
 */
export function useRevealSetting(target: SettingId | null) {
  useEffect(() => {
    if (!target) return;
    let tries = 0;
    let clear: ReturnType<typeof setTimeout> | undefined;
    const poll = setInterval(() => {
      const row = document.getElementById(settingAnchor(target));
      if (!row && ++tries < 20) return;
      clearInterval(poll);
      if (!row) return;
      row.scrollIntoView?.({ block: 'center', behavior: 'smooth' });
      row.classList.add('settings-row--found');
      clear = setTimeout(() => row.classList.remove('settings-row--found'), 2000);
    }, 50);
    return () => { clearInterval(poll); clearTimeout(clear); };
  }, [target]);
}
