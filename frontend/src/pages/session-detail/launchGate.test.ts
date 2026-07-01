import { describe, it, expect } from 'vitest';
import { canLaunchSession } from './launchGate';

const base = {
  portAvailable: false,
  hasPendingPrompt: false,
  tmuxAvailable: true,
  liveConnectionHint: true,
  directory: '/repo',
};

describe('canLaunchSession', () => {
  it('offers launch when all conditions hold', () => {
    expect(canLaunchSession(base)).toBe(true);
  });

  it('does not offer launch without a directory (dead-button guard)', () => {
    expect(canLaunchSession({ ...base, directory: '' })).toBe(false);
    expect(canLaunchSession({ ...base, directory: undefined })).toBe(false);
  });

  it('does not offer launch when opencode is already reachable', () => {
    expect(canLaunchSession({ ...base, portAvailable: true })).toBe(false);
  });

  it('does not offer launch without tmux, hint, or with a pending prompt', () => {
    expect(canLaunchSession({ ...base, tmuxAvailable: false })).toBe(false);
    expect(canLaunchSession({ ...base, liveConnectionHint: false })).toBe(false);
    expect(canLaunchSession({ ...base, hasPendingPrompt: true })).toBe(false);
  });
});
