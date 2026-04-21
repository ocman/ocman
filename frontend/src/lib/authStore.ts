import { create } from 'zustand';
import { api, AuthError, registerAuthErrorHandler } from './api';

/**
 * AuthStore tracks whether the server requires a password for this
 * client, and whether the client is currently authenticated.
 *
 * State lives in memory only — the cookie itself is the source of
 * truth across tabs and reloads; a persisted mirror would just
 * invite drift. The store exposes three lifecycle entry points:
 *
 *   bootstrap()   — call once at app mount; fetches /api/auth/me.
 *   login(pw)     — POST /api/auth/login; flips `authenticated` on
 *                   success.
 *   logout()      — POST /api/auth/logout; flips `authenticated` off
 *                   regardless of HTTP outcome (best-effort clear).
 *
 * The store also registers a global AuthError handler (via
 * registerAuthErrorHandler) so a 401 from any API call — typically
 * an expired cookie — immediately flips `authenticated` to false
 * and re-routes the SPA into the lockscreen.
 */
type AuthStore = {
  /** True until the first /api/auth/me response (or its failure). */
  checking: boolean;
  /** True when the server has a password configured. */
  authRequired: boolean;
  /** True when the current client has a valid cookie (or auth is off). */
  authenticated: boolean;
  /** Last login error to surface on the lockscreen, or null. */
  error: string | null;
  /** True while a /api/auth/login request is in flight. */
  submitting: boolean;

  bootstrap: () => Promise<void>;
  login: (password: string) => Promise<boolean>;
  logout: () => Promise<void>;
  /** Internal: flip to unauthenticated after observing a 401 mid-session. */
  handleAuthError: () => void;
};

export const useAuthStore = create<AuthStore>((set, get) => ({
  checking: true,
  authRequired: false,
  authenticated: true,
  error: null,
  submitting: false,

  bootstrap: async () => {
    try {
      const me = await api.authMe();
      set({
        checking: false,
        authRequired: me.authRequired,
        authenticated: me.authenticated,
      });
    } catch {
      // If /api/auth/me itself fails we fail open: assume no auth is
      // required so the app still loads on a legacy backend. The next
      // real API call will surface any 401 via handleAuthError.
      set({ checking: false, authRequired: false, authenticated: true });
    }
  },

  login: async (password: string) => {
    set({ submitting: true, error: null });
    try {
      await api.authLogin(password);
      set({ submitting: false, authenticated: true, error: null });
      return true;
    } catch (err) {
      const message = err instanceof AuthError ? 'Incorrect password.'
        : err instanceof Error ? err.message
        : 'Login failed.';
      set({ submitting: false, error: message });
      return false;
    }
  },

  logout: async () => {
    try {
      await api.authLogout();
    } catch {
      // Ignore — we're clearing local state regardless.
    }
    set({ authenticated: false, error: null });
  },

  handleAuthError: () => {
    // Only react once we actually know the server requires auth.
    // Before bootstrap completes, authRequired is false and a
    // spurious 401 (e.g. during a race with a just-logged-out
    // request) wouldn't flip us into the lockscreen unexpectedly.
    if (!get().authRequired) return;
    if (!get().authenticated) return;
    set({ authenticated: false });
  },
}));

/**
 * installAuthIntegration wires the store to the api layer. Idempotent;
 * called from main.tsx at boot.
 */
export function installAuthIntegration(): void {
  registerAuthErrorHandler(() => {
    useAuthStore.getState().handleAuthError();
  });
}
