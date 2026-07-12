// @vitest-environment jsdom
import { describe, it, expect, vi, afterEach } from 'vitest';
import { copyTextToClipboard } from './clipboard';

afterEach(() => {
  vi.restoreAllMocks();
  Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
});

describe('copyTextToClipboard', () => {
  it('uses the Clipboard API when available', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    expect(await copyTextToClipboard('hi')).toBe(true);
    expect(writeText).toHaveBeenCalledWith('hi');
  });

  it('falls back to execCommand when writeText rejects', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('denied'));
    Object.defineProperty(navigator, 'clipboard', { value: { writeText }, configurable: true });
    const exec = vi.fn().mockReturnValue(true);
    (document as unknown as { execCommand: typeof exec }).execCommand = exec;
    expect(await copyTextToClipboard('hi')).toBe(true);
    expect(exec).toHaveBeenCalledWith('copy');
  });

  it('returns false when no copy mechanism works', async () => {
    Object.defineProperty(navigator, 'clipboard', { value: undefined, configurable: true });
    (document as unknown as { execCommand: () => boolean }).execCommand = () => false;
    expect(await copyTextToClipboard('hi')).toBe(false);
  });
});
