// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fetchJSON } from './api.requests';
import { queryClient } from './queryClient';
import { KEYBINDINGS_QUERY_KEY, type Keybindings } from './keybindings';
import { useShortcut, useShortcutDispatcher, useShortcutLabel, useShortcutRegistry } from './shortcutRegistry';
import { KeyboardShortcutsDialog } from '../components/KeyboardShortcutsDialog';

vi.mock('./api.requests', () => ({ fetchJSON: vi.fn() }));

function Action({ id = 'test.action', handler = () => {} }: { id?: string; handler?: () => void }) {
  useShortcut({ id, scope: 'site', description: id, handler });
  return null;
}

function Dispatcher() {
  useShortcutDispatcher();
  return null;
}

function Hint() {
  return <span data-testid="hint">{useShortcutLabel('test.action')}</span>;
}

function press(code: string) {
  fireEvent.keyDown(window, { code, altKey: true });
}

beforeEach(() => {
  queryClient.clear();
  queryClient.setQueryDefaults(KEYBINDINGS_QUERY_KEY, { retry: false });
  useShortcutRegistry.setState({ shortcuts: new Map() });
  vi.mocked(fetchJSON).mockReset();
});

afterEach(() => {
  queryClient.setQueryDefaults(KEYBINDINGS_QUERY_KEY, { retry: undefined });
});

describe('backend keybindings', () => {
  it('waits for the catalog and uses backend bindings for dispatch, help and hints', async () => {
    let resolve!: (bindings: Keybindings) => void;
    const pending = new Promise<Keybindings>((done) => { resolve = done; });
    vi.mocked(fetchJSON).mockReturnValue(pending);
    const handler = vi.fn();
    render(<><Dispatcher /><Action handler={handler} /><Hint /><KeyboardShortcutsDialog open onClose={() => {}} /></>);
    expect(useShortcutRegistry.getState().shortcuts.size).toBe(0);
    expect(screen.getByTestId('hint')).toBeEmptyDOMElement();
    press('KeyZ');
    expect(handler).not.toHaveBeenCalled();

    await act(async () => resolve({ 'test.action': [{ code: 'KeyZ', alt: true }, { code: 'KeyX', alt: true }] }));
    await waitFor(() => expect(useShortcutRegistry.getState().shortcuts.has('test.action')).toBe(true));
    press('KeyZ');
    press('KeyX');
    press('KeyA');
    expect(handler).toHaveBeenCalledTimes(2);
    expect(screen.getByText('Z', { selector: 'kbd' })).toBeInTheDocument();
    expect(screen.getByTestId('hint').textContent).toMatch(/Z$/);
    expect(fetchJSON).toHaveBeenCalledExactlyOnceWith('/api/keybindings');
  });

  it('shares one request with simultaneous and later-mounted actions', async () => {
    vi.mocked(fetchJSON).mockResolvedValue({
      'test.action': [{ code: 'KeyZ', alt: true }],
      'test.other': [{ code: 'KeyX', alt: true }],
    });
    const view = render(<><Action /><Action id="test.other" /></>);
    await waitFor(() => expect(useShortcutRegistry.getState().shortcuts.size).toBe(2));
    view.rerender(<Action />);
    expect(useShortcutRegistry.getState().shortcuts.has('test.other')).toBe(false);
    view.rerender(<><Action /><Action id="test.other" /></>);
    expect(useShortcutRegistry.getState().shortcuts.size).toBe(2);
    expect(fetchJSON).toHaveBeenCalledTimes(1);
    view.unmount();
    expect(useShortcutRegistry.getState().shortcuts.size).toBe(0);
  });

  it('updates existing actions, ignores unknown IDs and disables empty or missing bindings', async () => {
    vi.mocked(fetchJSON).mockResolvedValue({ 'test.action': [{ code: 'KeyZ', alt: true }] });
    const handler = vi.fn();
    render(<><Dispatcher /><Action handler={handler} /></>);
    await waitFor(() => expect(useShortcutRegistry.getState().shortcuts.size).toBe(1));
    act(() => queryClient.setQueryData(KEYBINDINGS_QUERY_KEY, {
      'test.action': [{ code: 'KeyX', alt: true }], 'unknown.action': [{ code: 'KeyY', alt: true }],
    }));
    await waitFor(() => expect(useShortcutRegistry.getState().shortcuts.get('test.action')?.keys).toEqual([{ code: 'KeyX', alt: true }]));
    press('KeyZ');
    press('KeyY');
    expect(handler).not.toHaveBeenCalled();
    press('KeyX');
    expect(handler).toHaveBeenCalledTimes(1);
    act(() => queryClient.setQueryData(KEYBINDINGS_QUERY_KEY, { 'test.action': [] }));
    await waitFor(() => expect(useShortcutRegistry.getState().shortcuts.size).toBe(0));
    press('KeyX');
    expect(handler).toHaveBeenCalledTimes(1);
    act(() => queryClient.setQueryData(KEYBINDINGS_QUERY_KEY, {}));
    await waitFor(() => expect(useShortcutRegistry.getState().shortcuts.size).toBe(0));
  });

  it('leaves actions inactive on failure and recovers on a successful refetch', async () => {
    vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('unavailable'));
    const handler = vi.fn();
    render(<><Dispatcher /><Action handler={handler} /></>);
    await waitFor(() => expect(queryClient.getQueryState(KEYBINDINGS_QUERY_KEY)?.status).toBe('error'));
    expect(useShortcutRegistry.getState().shortcuts.size).toBe(0);
    press('KeyZ');
    expect(handler).not.toHaveBeenCalled();
    vi.mocked(fetchJSON).mockResolvedValue({ 'test.action': [{ code: 'KeyZ', alt: true }] });
    await act(async () => { await queryClient.refetchQueries({ queryKey: KEYBINDINGS_QUERY_KEY }); });
    await waitFor(() => expect(useShortcutRegistry.getState().shortcuts.size).toBe(1));
    press('KeyZ');
    expect(handler).toHaveBeenCalledTimes(1);
  });
});
