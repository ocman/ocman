// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';

import { useUiStore } from '../../lib/uiStore';
import { useMobilePanel } from './useMobilePanel';
import { MemoryRouter, useNavigate } from 'react-router-dom';

describe('useMobilePanel', () => {
  beforeEach(() => useUiStore.setState({ changesSidebarOpenTabs: [] }));

  it('toggles panels, seeds a right-panel tab, and closes on Escape and route change', () => {
    const { result, rerender } = renderHook((id: string) => useMobilePanel(id), { initialProps: 's1', wrapper: MemoryRouter });
    expect(result.current.mobilePanel).toBeNull();

    act(() => result.current.toggleMobileSidebar());
    expect(result.current.mobilePanel).toBe('sidebar');
    act(() => result.current.toggleMobileSidebar());
    expect(result.current.mobilePanel).toBeNull();

    act(() => result.current.toggleMobileDetails());
    expect(result.current.mobilePanel).toBe('details');
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['info']);

    act(() => window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' })));
    expect(result.current.mobilePanel).toBeNull();

    act(() => result.current.toggleMobileSidebar());
    rerender('s2');
    expect(result.current.mobilePanel).toBeNull();

    act(() => result.current.toggleMobileSidebar());
    act(() => result.current.closeMobilePanel());
    expect(result.current.mobilePanel).toBeNull();
  });

  it('closes the mobile drawer when navigating between prepared draft identities', () => {
    const { result } = renderHook(() => ({ panel: useMobilePanel('new'), navigate: useNavigate() }), { wrapper: MemoryRouter });
    act(() => result.current.panel.toggleMobileSidebar());
    act(() => result.current.navigate('/session/new?dir=%2Frepo&draftId=second'));
    expect(result.current.panel.mobilePanel).toBeNull();
    act(() => result.current.panel.toggleMobileSidebar());
    act(() => result.current.navigate('/session/new?dir=%2Frepo&draftId=second'));
    expect(result.current.panel.mobilePanel).toBeNull();
  });
});
