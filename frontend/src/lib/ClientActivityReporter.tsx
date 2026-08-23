import { useEffect } from 'react';
import { api, type ClientActivity } from './api';
import { activityScopeSnapshot, subscribeActivityScopes } from './activityScopes';

export const ACTIVITY_HEARTBEAT_MS = 25_000;
export const ACTIVITY_TTL_MS = 45_000;
export const RECENT_INTERACTION_MS = 30_000;

const clientId = typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
  ? crypto.randomUUID()
  : `tab-${Date.now().toString(36)}-${Math.random().toString(36).slice(2)}`;

export function ClientActivityReporter() {
  useEffect(() => {
    let mounted = true;
    let inFlight = false;
    let pending = false;
    let recentlyInteracted = false;
    let interactionTimer: ReturnType<typeof setTimeout> | undefined;

    const payload = (): ClientActivity => ({
      clientId,
      visible: !document.hidden,
      focused: document.hasFocus(),
      recentlyInteracted,
      scopes: activityScopeSnapshot(),
      ttlMs: ACTIVITY_TTL_MS,
    });
    const send = () => {
      if (!mounted) return;
      if (inFlight) {
        pending = true;
        return;
      }
      inFlight = true;
      void api.clientActivity(payload()).catch(() => {}).finally(() => {
        inFlight = false;
        if (pending) {
          pending = false;
          send();
        }
      });
    };
    const onInteraction = () => {
      if (interactionTimer) clearTimeout(interactionTimer);
      interactionTimer = setTimeout(() => {
        recentlyInteracted = false;
        send();
      }, RECENT_INTERACTION_MS);
      if (recentlyInteracted) return;
      recentlyInteracted = true;
      send();
    };

    const unsubscribeScopes = subscribeActivityScopes(send);
    document.addEventListener('visibilitychange', send);
    window.addEventListener('focus', send);
    window.addEventListener('blur', send);
    window.addEventListener('pointerdown', onInteraction);
    window.addEventListener('keydown', onInteraction);
    const heartbeat = setInterval(send, ACTIVITY_HEARTBEAT_MS);
    send();

    return () => {
      mounted = false;
      pending = false;
      unsubscribeScopes();
      document.removeEventListener('visibilitychange', send);
      window.removeEventListener('focus', send);
      window.removeEventListener('blur', send);
      window.removeEventListener('pointerdown', onInteraction);
      window.removeEventListener('keydown', onInteraction);
      clearInterval(heartbeat);
      if (interactionTimer) clearTimeout(interactionTimer);
    };
  }, []);

  return null;
}
