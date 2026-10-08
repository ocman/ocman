// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { usePanelVisible } from './usePanelVisible';

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

it('matches closed mobile drawers, desktop layout, and document visibility', () => {
  let hidden = false;
  vi.stubGlobal('innerWidth', 390);
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const { result, rerender } = renderHook(({ open }) => usePanelVisible(open), { initialProps: { open: false } });
  expect(result.current).toBe(false);
  rerender({ open: true });
  expect(result.current).toBe(true);
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  expect(result.current).toBe(false);
  rerender({ open: false });
  act(() => { vi.stubGlobal('innerWidth', 1024); window.dispatchEvent(new Event('resize')); });
  expect(result.current).toBe(false);
  act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
  expect(result.current).toBe(true);
});
