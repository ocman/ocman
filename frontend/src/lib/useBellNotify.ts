import { useEffect, useRef } from 'react';
import { notifyStateKey, useNotifyBaseline } from './useNotifyBaseline';
import { useUiStore } from './uiStore';

/**
 * Plays a short bell tone using the Web Audio API.
 * Two-tone chime: a higher note followed by a lower one.
 */
function playBell() {
  try {
    const ctx = new AudioContext();

    function tone(freq: number, startTime: number, duration: number, gain: number) {
      const osc = ctx.createOscillator();
      const gainNode = ctx.createGain();
      osc.connect(gainNode);
      gainNode.connect(ctx.destination);
      osc.type = 'sine';
      osc.frequency.setValueAtTime(freq, startTime);
      gainNode.gain.setValueAtTime(0, startTime);
      gainNode.gain.linearRampToValueAtTime(gain, startTime + 0.01);
      gainNode.gain.exponentialRampToValueAtTime(0.001, startTime + duration);
      osc.start(startTime);
      osc.stop(startTime + duration);
    }

    const now = ctx.currentTime;
    tone(880, now, 0.6, 0.25);
    tone(660, now + 0.18, 0.7, 0.2);

    // Close the context once both tones have finished.
    setTimeout(() => { void ctx.close(); }, 1200);
  } catch {
    // Web Audio not available — silently skip.
  }
}

/**
 * Plays a bell sound when the document is hidden and a session becomes
 * done/error or asks a question/permission prompt.
 *
 * Controlled by the `bellEnabled` setting in uiStore.
 *
 * Consumes the shared `useNotifyStore` (via useNotifyBaseline) instead
 * of polling `/api/sessions/notify` independently (P2 fix).
 */
export function useBellNotify() {
  const bellEnabled = useUiStore((s) => s.bellEnabled);
  const bellEnabledRef = useRef(bellEnabled);
  useEffect(() => {
    bellEnabledRef.current = bellEnabled;
  }, [bellEnabled]);

  useNotifyBaseline((sessions, baseline) => {
    if (!bellEnabledRef.current) return false;
    if (!document.hidden) return false;

    const hasNew = sessions.some((s) => {
      const key = notifyStateKey(s);
      if (baseline !== null && baseline.get(s.id) === key) return false;
      return true;
    });

    if (hasNew) {
      playBell();
      // Ask the hook to re-snapshot the baseline so we don't ring again
      // for the same events.
      return true;
    }
    return false;
  });
}
