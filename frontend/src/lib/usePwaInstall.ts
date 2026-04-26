import { useEffect, useState } from 'react';

/**
 * PWA install state hook.
 *
 * Captures Chromium's `beforeinstallprompt` event so the UI can offer
 * an in-app "Install" button. On other browsers (Safari, Firefox) the
 * event never fires and `canInstall` stays false — the rest of the app
 * keeps working as a normal web app, which is the whole point of this
 * being purely additive.
 *
 * State machine:
 *   - canInstall=false, installed=false  → fresh tab in a non-Chromium
 *     browser, or before the browser has decided the page is
 *     installable.
 *   - canInstall=true,  installed=false  → browser fired
 *     beforeinstallprompt; we cached it. UI may show the button.
 *   - canInstall=false, installed=true   → user installed the app
 *     (either via our button or the browser's URL-bar affordance), or
 *     the page is currently being rendered inside the installed
 *     standalone window.
 *
 * Implementation notes:
 *   - The captured event is a single-shot. Chromium will only re-fire
 *     it after the user uninstalls, so once we've called .prompt() (or
 *     the user dismissed it) we have to discard our reference.
 *   - State is held at module scope so multiple components rendering
 *     this hook share one source of truth (e.g. a future header
 *     button + the settings page button), and so the captured event
 *     survives unmount/remount of any single consumer.
 *   - All DOM interaction lives in `bindOnce()`. Everything else
 *     (capture, prompt, install, reset) operates on plain module
 *     state, which is what the unit tests exercise — the project
 *     deliberately doesn't pull in jsdom or @testing-library/react.
 */

// Chromium-only event type. Not in the standard lib.d.ts so we declare
// just the bits we touch.
type BeforeInstallPromptEvent = Event & {
  prompt: () => Promise<void>;
  userChoice: Promise<{ outcome: 'accepted' | 'dismissed' }>;
};

export type PwaInstallState = {
  canInstall: boolean;
  installed: boolean;
  promptInstall: () => Promise<void>;
};

// Module-scope state + listeners so multiple hook consumers share the
// same captured event and stay in sync.
let cachedPrompt: BeforeInstallPromptEvent | null = null;
let installed = detectStandalone();
const subscribers = new Set<() => void>();

function notify() {
  for (const fn of subscribers) fn();
}

function detectStandalone(): boolean {
  if (typeof window === 'undefined') return false;
  // Chromium / Edge / desktop PWAs.
  if (window.matchMedia?.('(display-mode: standalone)').matches) return true;
  // iOS Safari home-screen webapp.
  const nav = window.navigator as Navigator & { standalone?: boolean };
  if (nav.standalone === true) return true;
  return false;
}

/**
 * Pure-JS controller entry points. The DOM listeners in `bindOnce`
 * forward straight to these; tests call them directly without needing
 * a window.
 */
export function _capturePrompt(e: BeforeInstallPromptEvent) {
  // Suppress Chrome's mini-infobar; we want to control the prompt
  // timing via our own button.
  e.preventDefault?.();
  cachedPrompt = e;
  notify();
}

export function _markInstalled() {
  cachedPrompt = null;
  installed = true;
  notify();
}

let bound = false;
function bindOnce() {
  if (bound || typeof window === 'undefined') return;
  window.addEventListener('beforeinstallprompt', (e) =>
    _capturePrompt(e as BeforeInstallPromptEvent),
  );
  window.addEventListener('appinstalled', () => _markInstalled());
  bound = true;
}

async function promptInstall(): Promise<void> {
  const evt = cachedPrompt;
  if (!evt) return;
  // Discard the cached event regardless of outcome — Chromium won't
  // fire it again until the user uninstalls.
  cachedPrompt = null;
  try {
    await evt.prompt();
    await evt.userChoice;
  } finally {
    notify();
  }
}

function snapshot(): PwaInstallState {
  return {
    canInstall: cachedPrompt !== null && !installed,
    installed,
    promptInstall,
  };
}

/**
 * Test-only: reset module state so each test starts from a clean
 * slate. Underscore-prefixed to signal "not part of the public
 * surface" — only the test file should touch this.
 */
export const __resetForTests = (opts?: { standalone?: boolean }) => {
  cachedPrompt = null;
  installed = opts?.standalone ?? false;
  subscribers.clear();
};

/** Test-only: read current state without React. */
export const __peekForTests = snapshot;

export function usePwaInstall(): PwaInstallState {
  bindOnce();
  const [, setTick] = useState(0);

  useEffect(() => {
    const fn = () => setTick((n) => n + 1);
    subscribers.add(fn);
    return () => {
      subscribers.delete(fn);
    };
  }, []);

  return snapshot();
}
