import { useEffect, useRef } from 'react';
import type { NotifyEntry } from './api';
import { useNotifyStore } from './useNotifyData';
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

/** Produce a stable string key from a session's notification-relevant state. */
function stateKey(s: { id: string; status: string; pendingPermission?: boolean; pendingQuestion?: boolean }): string {
  return `${s.status}|${s.pendingPermission ? '1' : '0'}|${s.pendingQuestion ? '1' : '0'}`;
}

/**
 * Plays a bell sound when the document is hidden and a session becomes
 * done/error or asks a question/permission prompt.
 *
 * Controlled by the `bellEnabled` setting in uiStore.
 *
 * Now consumes the shared `useNotifyStore` instead of polling
 * `/api/sessions/notify` independently (P2 fix).
 */
export function useBellNotify() {
  const bellEnabled = useUiStore((s) => s.bellEnabled);
  // Snapshot of (id → status|pending) taken when the tab goes hidden, so we
  // only ring for *new* events rather than pre-existing ones.
  const baselineRef = useRef<Map<string, string> | null>(null);
  const bellEnabledRef = useRef(bellEnabled);

  // Keep the ref in sync so the store subscription closure always reads the latest value.
  useEffect(() => {
    bellEnabledRef.current = bellEnabled;
  }, [bellEnabled]);

  useEffect(() => {
    // Subscribe to the shared notify store.
    useNotifyStore.getState().subscribe();

    function checkBell(sessions: NotifyEntry[]) {
      if (!bellEnabledRef.current) return;
      if (!document.hidden) return;

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
    }

    function onVisibilityChange() {
      const sessions = useNotifyStore.getState().data;
      if (!document.hidden) {
        // Tab visible — reset baseline so the next hide cycle starts fresh.
        baselineRef.current = null;
      } else {
        // Tab hidden — snapshot current state, then check once.
        if (sessions) {
          baselineRef.current = new Map(sessions.map((s) => [s.id, stateKey(s)]));
          checkBell(sessions);
        }
      }
    }

    document.addEventListener('visibilitychange', onVisibilityChange);

    // If already hidden when the hook mounts, take a baseline immediately.
    const initial = useNotifyStore.getState().data;
    if (document.hidden && initial) {
      baselineRef.current = new Map(initial.map((s) => [s.id, stateKey(s)]));
    }

    // Subscribe to store changes to react when new data arrives.
    const unsub = useNotifyStore.subscribe((state) => {
      if (state.data) checkBell(state.data);
    });

    return () => {
      unsub();
      document.removeEventListener('visibilitychange', onVisibilityChange);
      useNotifyStore.getState().unsubscribe();
    };
  }, []);
}
