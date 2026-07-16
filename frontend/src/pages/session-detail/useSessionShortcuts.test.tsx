// @vitest-environment jsdom

import { renderHook } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { useShortcutRegistry } from '../../lib/shortcutRegistry';
import { useSessionShortcuts } from './useSessionShortcuts';

afterEach(() => useShortcutRegistry.setState({ shortcuts: new Map() }));

describe('useSessionShortcuts', () => {
  it('registers Alt+G to open the user-message picker', () => {
    const openMessageJumpPicker = vi.fn();
    renderHook(() => useSessionShortcuts({
      session: null,
      portAvailable: false,
      matchingTmuxSession: undefined,
      jumpToSession: vi.fn(),
      handleTmuxShortcut: vi.fn(),
      handleVSCodeShortcut: vi.fn(),
      handleNewSession: vi.fn(),
      openMessageJumpPicker,
    }));

    const shortcut = useShortcutRegistry.getState().shortcuts.get('session.jump-to-message');
    expect(shortcut?.keys).toEqual({ code: 'KeyG', alt: true });
    shortcut?.handler(new KeyboardEvent('keydown'));
    expect(openMessageJumpPicker).toHaveBeenCalledOnce();
  });
});
