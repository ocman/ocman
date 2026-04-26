import { describe, it, expect, beforeEach, vi } from 'vitest';
import {
  __peekForTests,
  __resetForTests,
  _capturePrompt,
  _markInstalled,
} from './usePwaInstall';

// We exercise the module-level controller (the same surface the
// window listeners forward to), not the React hook. The project
// deliberately avoids @testing-library/react and jsdom — see
// useCapabilities.test.ts for the same pattern.

function makePromptEvent(outcome: 'accepted' | 'dismissed' = 'accepted') {
  const prompt = vi.fn().mockResolvedValue(undefined);
  return {
    // Cast: the controller only touches these three fields.
    event: {
      preventDefault: vi.fn(),
      prompt,
      userChoice: Promise.resolve({ outcome }),
    } as unknown as Parameters<typeof _capturePrompt>[0],
    prompt,
  };
}

describe('usePwaInstall controller', () => {
  beforeEach(() => {
    __resetForTests();
  });

  it('starts with canInstall=false and installed=false', () => {
    const s = __peekForTests();
    expect(s.canInstall).toBe(false);
    expect(s.installed).toBe(false);
  });

  it('flips canInstall=true when a beforeinstallprompt event is captured', () => {
    const { event } = makePromptEvent();
    _capturePrompt(event);
    expect(__peekForTests().canInstall).toBe(true);
  });

  it('preventDefault is called on the captured event to suppress the mini-infobar', () => {
    const { event } = makePromptEvent();
    _capturePrompt(event);
    expect((event as unknown as { preventDefault: () => void }).preventDefault).toHaveBeenCalledOnce();
  });

  it('promptInstall() invokes the captured event and clears canInstall on accept', async () => {
    const { event, prompt } = makePromptEvent('accepted');
    _capturePrompt(event);
    expect(__peekForTests().canInstall).toBe(true);

    await __peekForTests().promptInstall();

    expect(prompt).toHaveBeenCalledOnce();
    expect(__peekForTests().canInstall).toBe(false);
  });

  it('promptInstall() also clears canInstall on dismiss (event is single-use)', async () => {
    const { event } = makePromptEvent('dismissed');
    _capturePrompt(event);
    await __peekForTests().promptInstall();
    expect(__peekForTests().canInstall).toBe(false);
  });

  it('promptInstall() is a no-op when no prompt has been captured', async () => {
    await expect(__peekForTests().promptInstall()).resolves.toBeUndefined();
    expect(__peekForTests().canInstall).toBe(false);
  });

  it('appinstalled flips installed=true and clears canInstall', () => {
    const { event } = makePromptEvent();
    _capturePrompt(event);
    expect(__peekForTests().canInstall).toBe(true);

    _markInstalled();

    const s = __peekForTests();
    expect(s.installed).toBe(true);
    expect(s.canInstall).toBe(false);
  });

  it('canInstall stays false when already installed, even if a prompt event sneaks in', () => {
    // E.g. the user installed via the URL-bar affordance and Chromium
    // fired one last beforeinstallprompt before the appinstalled event
    // landed. The button shouldn't reappear.
    __resetForTests({ standalone: true });
    const { event } = makePromptEvent();
    _capturePrompt(event);
    expect(__peekForTests().installed).toBe(true);
    expect(__peekForTests().canInstall).toBe(false);
  });
});
