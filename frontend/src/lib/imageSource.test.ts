// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { imageSource } from './imageSource';

describe('image source classification', () => {
  it.each(['', '/local.svg', 'data:image/svg+xml,<svg/>'])('keeps safe local or embedded source %s', src => {
    expect(imageSource(src)).toBe('local');
  });
  it('allows only blobs belonging to this origin', () => {
    expect(imageSource(`blob:${window.location.origin}/id`)).toBe('local');
    expect(imageSource('blob:https://elsewhere.invalid/id')).toBe('blocked');
  });
  it.each(['data:text/html,<img/>', 'file:///etc/passwd', 'javascript:alert(1)', 'http://%invalid', 'http://user:pass@example.com/x'])('rejects unsafe source %s', src => {
    expect(imageSource(src)).toBe('blocked');
  });
  it('normalizes external URLs for the consent wrapper', () => {
    const result = imageSource('//images.example/report.png');
    expect(result).toBeInstanceOf(URL);
    expect((result as URL).hostname).toBe('images.example');
  });
});
