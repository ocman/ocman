// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useSidebarFilter } from './useSidebarFilter';

beforeEach(() => {
  const values = new Map<string, string>();
  vi.stubGlobal('localStorage', {
    getItem: vi.fn((key: string) => values.get(key) ?? null),
    setItem: vi.fn((key: string, value: string) => values.set(key, value)),
  });
});

it.each([['archived', false], ['children', true], ['factory', false], ['routines', false]] as const)(
  'restores the %s filter after remounting', (name, initial) => {
    const first = renderHook(() => useSidebarFilter(name, initial));
    expect(first.result.current[0]).toBe(initial);
    act(() => first.result.current[1]((current) => !current));
    first.unmount();
    const second = renderHook(() => useSidebarFilter(name, initial));
    expect(second.result.current[0]).toBe(!initial);
  },
);

it('ignores malformed stored values', () => {
  localStorage.setItem('ocman:sidebar-filter:children', 'garbage');
  expect(renderHook(() => useSidebarFilter('children', true)).result.current[0]).toBe(true);
});

it('keeps filters usable when storage is unavailable', () => {
  vi.mocked(localStorage.getItem).mockImplementation(() => { throw new Error('blocked'); });
  vi.mocked(localStorage.setItem).mockImplementation(() => { throw new Error('full'); });
  const { result } = renderHook(() => useSidebarFilter('factory', false));
  act(() => result.current[1](true));
  expect(result.current[0]).toBe(true);
});
