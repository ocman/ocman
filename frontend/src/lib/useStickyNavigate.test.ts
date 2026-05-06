// @vitest-environment jsdom
//
// Tests for the pure helper underlying useStickyNavigate. We don't
// exercise the hook itself here — react-router-dom's useNavigate is
// well-tested upstream; the only logic worth pinning is the URL
// rewrite that merges sticky params.

import { describe, expect, it } from 'vitest';
import { applyStickyParams } from './useStickyNavigate';

describe('applyStickyParams', () => {
  it('returns the input unchanged when there are no sticky params', () => {
    expect(applyStickyParams('/session/abc', '?debug', [])).toBe('/session/abc');
  });

  it('returns the input unchanged when current search has nothing to inherit', () => {
    expect(applyStickyParams('/session/abc', '?other=1', ['debug'])).toBe('/session/abc');
  });

  it('appends a sticky param that is set without a value (?debug)', () => {
    expect(applyStickyParams('/session/abc', '?debug', ['debug'])).toBe('/session/abc?debug=');
  });

  it('appends a sticky param with its value preserved', () => {
    expect(applyStickyParams('/session/abc', '?debug=verbose', ['debug']))
      .toBe('/session/abc?debug=verbose');
  });

  it('does not overwrite a sticky param the caller already set', () => {
    expect(applyStickyParams('/session/abc?debug=local', '?debug=remote', ['debug']))
      .toBe('/session/abc?debug=local');
  });

  it('preserves the hash fragment', () => {
    expect(applyStickyParams('/session/abc#top', '?debug', ['debug']))
      .toBe('/session/abc?debug=#top');
  });

  it('preserves the existing query string and appends sticky', () => {
    expect(applyStickyParams('/session/abc?foo=1', '?debug', ['debug']))
      .toBe('/session/abc?foo=1&debug=');
  });

  it('handles multiple sticky params at once', () => {
    expect(applyStickyParams('/x', '?debug&trace=1&other=skip', ['debug', 'trace']))
      .toBe('/x?debug=&trace=1');
  });

  it('handles an empty current search gracefully', () => {
    expect(applyStickyParams('/x', '', ['debug'])).toBe('/x');
  });

  it('handles current search without leading ?', () => {
    // URLSearchParams accepts both forms.
    expect(applyStickyParams('/x', 'debug=verbose', ['debug']))
      .toBe('/x?debug=verbose');
  });
});
