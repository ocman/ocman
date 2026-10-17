// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { usePhoneOverlay } from './usePhoneOverlay';

let phone = true;
vi.mock('./useIsPhone', () => ({ useIsPhone: () => phone }));

function wrapper({ children }: { children: React.ReactNode }) {
  return <MemoryRouter initialEntries={['/projects', { pathname: '/sessions', search: '?q=hi', hash: '#list', state: { previous: 'preserved' } }]}>{children}</MemoryRouter>;
}

function useSubject() {
  const [value, set] = usePhoneOverlay<'more' | 'details'>('test');
  return { value, set, location: useLocation(), navigate: useNavigate() };
}

describe('usePhoneOverlay', () => {
  beforeEach(() => { phone = true; });

  it('preserves URL and route state, back dismisses and forward reopens', () => {
    const { result } = renderHook(useSubject, { wrapper });
    act(() => result.current.set('more'));
    expect(result.current.value).toBe('more');
    expect(result.current.location).toMatchObject({ pathname: '/sessions', search: '?q=hi', hash: '#list', state: { previous: 'preserved' } });
    act(() => result.current.navigate(-1));
    expect(result.current.value).toBeNull();
    expect(result.current.location.pathname).toBe('/sessions');
    act(() => result.current.navigate(1));
    expect(result.current.value).toBe('more');
  });

  it('replaces panel switches and consumes explicit dismissal with no extra back step', () => {
    const { result } = renderHook(useSubject, { wrapper });
    act(() => result.current.set('more'));
    act(() => result.current.set('more'));
    act(() => result.current.set('details'));
    expect(result.current.value).toBe('details');
    act(() => result.current.set(null));
    expect(result.current.value).toBeNull();
    act(() => result.current.navigate(-1));
    expect(result.current.location.pathname).toBe('/projects');
  });

  it('never goes back on a direct link without an overlay entry', () => {
    const { result } = renderHook(useSubject, { wrapper });
    act(() => result.current.set(null));
    expect(result.current.location.pathname).toBe('/sessions');
  });

  it('ignores overlay state belonging to another route or owner', () => {
    const { result } = renderHook(useSubject, { wrapper });
    act(() => result.current.navigate('/inbox', { state: { phoneOverlay: { name: 'other', value: 'more', scope: '/inbox' } } }));
    expect(result.current.value).toBeNull();
    act(() => result.current.navigate('/settings', { state: { phoneOverlay: { name: 'test', value: 'more', scope: '/inbox' } } }));
    expect(result.current.value).toBeNull();
    act(() => result.current.set('more'));
    act(() => result.current.navigate('/projects'));
    expect(result.current.value).toBeNull();
  });

  it('does not change desktop history', () => {
    phone = false;
    const { result } = renderHook(useSubject, { wrapper });
    const key = result.current.location.key;
    act(() => result.current.set('more'));
    expect(result.current.value).toBeNull();
    expect(result.current.location.key).toBe(key);
  });
});
