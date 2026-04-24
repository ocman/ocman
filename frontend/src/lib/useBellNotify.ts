import { useEffect, useRef } from 'react';
import { api } from './api';
import { useUiStore } from './uiStore';

const POLL_INTERVAL_MS = 10_000;
const BELL_LOOKBACK_MS = 7 * 24 * 60 * 60 * 1000;
const BELL_LIMIT = 500;

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
 * Polls session notify state in the background (same cadence as useFaviconNotify)
 * and plays a bell sound when the document is hidden and a session becomes
 * done/error or asks a question/permission prompt.
 *
 * Controlled by the `bellEnabled` setting in uiStore.
 */
export function useBellNotify() {
  const bellEnabled = useUiStore((s) => s.bellEnabled);
  // Snapshot of (id → status|pending) taken when the tab goes hidden, so we
  // only ring for *new* events rather than pre-existing ones.
  const baselineRef = useRef<Map<string, string> | null>(null);
  const bellEnabledRef = useRef(bellEnabled);

  // Keep the ref in sync so the interval closure always reads the latest value.
  useEffect(() => {
    bellEnabledRef.current = bellEnabled;
  }, [bellEnabled]);

  useEffect(() => {
    async function takeBaseline() {
      try {
        const sessions = await api.sessionsNotify({
          since: Date.now() - BELL_LOOKBACK_MS,
          limit: BELL_LIMIT,
        });
        baselineRef.current = new Map(sessions.map((s) => [s.id, stateKey(s)]));
      } catch {
        baselineRef.current = null;
      }
    }

    async function checkBell() {
      if (!bellEnabledRef.current) return;
      if (!document.hidden) return;

      try {
        const sessions = await api.sessionsNotify({
          since: Date.now() - BELL_LOOKBACK_MS,
          limit: BELL_LIMIT,
        });

        const baseline = baselineRef.current;
        const hasNew = sessions.some((s) => {
          const key = stateKey(s);
          if (baseline !== null && baseline.get(s.id) === key) return false;
          return true;
        });

        if (hasNew) {
          playBell();
          // Update baseline so we don't ring again for the same events.
          baselineRef.current = new Map(sessions.map((s) => [s.id, stateKey(s)]));
        }
      } catch {
        // silently ignore
      }
    }

    function onVisibilityChange() {
      if (!document.hidden) {
        // Tab visible — reset baseline so the next hide cycle starts fresh.
        baselineRef.current = null;
      } else {
        // Tab hidden — snapshot current state, then check once.
        void takeBaseline().then(() => void checkBell());
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    // If already hidden when the hook mounts, take a baseline immediately.
    if (document.hidden) {
      void takeBaseline();
    }

    const id = setInterval(() => void checkBell(), POLL_INTERVAL_MS);

    return () => {
      clearInterval(id);
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, []);
}

/** Produce a stable string key from a session's notification-relevant state. */
function stateKey(s: { id: string; status: string; pendingPermission?: boolean; pendingQuestion?: boolean }): string {
  return `${s.status}|${s.pendingPermission ? '1' : '0'}|${s.pendingQuestion ? '1' : '0'}`;
}
