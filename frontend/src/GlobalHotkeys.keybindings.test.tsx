// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { GlobalHotkeys } from './App';
import { queryClient } from './lib/queryClient';
import { KEYBINDINGS_QUERY_KEY } from './lib/keybindings';

vi.mock('./components/CommandPalette', () => ({ CommandPalette: () => null }));
vi.mock('./components/WorktreeFormModal', () => ({ WorktreeFormModal: () => null }));
vi.mock('./components/KeyboardShortcutsDialog', () => ({ KeyboardShortcutsDialog: () => null }));

function bindings() {
  queryClient.setQueryData(KEYBINDINGS_QUERY_KEY, {
    'site.scroll-down': [{ code: 'KeyD', alt: true }],
    'site.scroll-up': [{ code: 'KeyU', alt: true }],
  });
}

it('keeps scroll direction attached to the action when physical keys change', () => {
  bindings();
  render(<MemoryRouter><GlobalHotkeys /><div data-testid="scroller" tabIndex={0} style={{ overflowY: 'auto' }} /></MemoryRouter>);
  const scroller = screen.getByTestId('scroller');
  Object.defineProperties(scroller, { clientHeight: { value: 400 }, scrollHeight: { value: 2000 } });
  const scroll = vi.fn();
  scroller.scrollBy = scroll;
  scroller.focus();
  fireEvent.keyDown(scroller, { code: 'KeyD', altKey: true });
  fireEvent.keyDown(scroller, { code: 'KeyU', altKey: true });
  fireEvent.keyDown(scroller, { code: 'ArrowDown', altKey: true });
  expect(scroll.mock.calls).toEqual([
    [{ top: 200, behavior: 'smooth' }],
    [{ top: -200, behavior: 'smooth' }],
  ]);
});

it('uses the conversation viewport when focus is outside a scrolling element', () => {
  bindings();
  render(<MemoryRouter><GlobalHotkeys /><div className="oc-thread-viewport" data-testid="thread" /></MemoryRouter>);
  const thread = screen.getByTestId('thread');
  Object.defineProperty(thread, 'clientHeight', { value: 300 });
  const scroll = vi.fn();
  thread.scrollBy = scroll;
  fireEvent.keyDown(window, { code: 'KeyD', altKey: true });
  expect(scroll).toHaveBeenCalledExactlyOnceWith({ top: 150, behavior: 'smooth' });
});
