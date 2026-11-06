// @vitest-environment jsdom
import { act, fireEvent, render, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { PluginNavIcon } from './PluginNavIcon';
import { bootstrapIconName, pluginIconURL } from '../lib/pluginIcons';

const fetchMock = vi.fn();
const createURL = vi.fn(() => 'blob:icon-test');
const revokeURL = vi.fn();
beforeEach(() => {
  vi.stubGlobal('fetch', fetchMock);
  URL.createObjectURL = createURL;
  URL.revokeObjectURL = revokeURL;
  fetchMock.mockResolvedValue({ ok: true, headers: new Headers({ 'Content-Type': 'image/svg+xml' }), arrayBuffer: async () => new TextEncoder().encode('<svg/>').buffer });
});
afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });

it('keeps puzzle by default and for unknown or mixed Bootstrap names', () => {
  for (const icon of [undefined, { bootstrap: 'not-an-installed-icon' }, { bootstrap: 'journal-text', path: 'a.svg' }, { bootstrap: 'journal-text other-class' }]) {
    const view = render(<PluginNavIcon ownerId="local" pluginId="org.example.docs" icon={icon} />);
    expect(view.container.querySelector('i')).toHaveClass('bi-puzzle');
    view.unmount();
  }
  expect(fetchMock).not.toHaveBeenCalled();
});

it('renders installed Bootstrap names without fetching an asset', () => {
  const view = render(<PluginNavIcon ownerId="local" pluginId="org.example.docs" icon={{ bootstrap: 'journal-text' }} />);
  expect(view.container.querySelector('i')).toHaveClass('bi-journal-text');
  expect(view.container.querySelector('i')).toHaveAttribute('aria-hidden', 'true');
  expect(fetchMock).not.toHaveBeenCalled();
  expect(bootstrapIconName({ bootstrap: '__proto__' })).toBe('puzzle');
});

it('rejects unsafe paths before a request and encodes installation identity', () => {
  for (const path of ['/a.svg', '../a.svg', 'a/../b.svg', 'a//b.svg', './a.svg', 'https://evil/x.svg', 'a.svg?x', 'a.svg#x', 'a%2fb.svg', 'a\\b.svg', 'a.png', 'a'.repeat(257) + '.svg']) {
    expect(pluginIconURL('local', 'x', { path })).toBeUndefined();
  }
  expect(pluginIconURL('owner&x', 'org.example.tools', { path: 'assets/wrench.svg' })).toBe('/api/plugins/icon?ownerId=owner%26x&pluginId=org.example.tools&path=assets%2Fwrench.svg');
});

it('preloads validated SVG as an image then masks with a blob URL, never inline markup', async () => {
  const view = render(<PluginNavIcon ownerId="machine" pluginId="org.example.tools" icon={{ path: 'assets/wrench.svg' }} />);
  expect(view.container.querySelector('.bi-puzzle')).toBeInTheDocument();
  await waitFor(() => expect(view.container.querySelector('img')).toHaveAttribute('src', 'blob:icon-test'));
  const image = view.container.querySelector('img')!;
  fireEvent.load(image);
  expect(view.container.querySelector('.plugin-nav-icon-mask')).toHaveStyle({ maskImage: 'url("blob:icon-test")' });
  expect(view.container.querySelector('svg')).toBeNull();
  expect(view.container.querySelector('.bi-puzzle')).toBeNull();
  expect(fetchMock).toHaveBeenCalledWith(expect.stringContaining('ownerId=machine'), expect.objectContaining({ credentials: 'same-origin', redirect: 'error' }));
  fireEvent.error(image);
  expect(view.container.querySelector('.bi-puzzle')).toBeInTheDocument();
  view.unmount();
  expect(revokeURL).toHaveBeenCalledWith('blob:icon-test');
});

it.each(['offline', 'missing', 'type', 'empty', 'oversize'])('retains puzzle on %s assets', async (failure) => {
  if (failure === 'offline') fetchMock.mockRejectedValueOnce(new Error('offline'));
  else fetchMock.mockResolvedValueOnce({ ok: failure !== 'missing', headers: new Headers({ 'Content-Type': failure === 'type' ? 'text/html' : 'image/svg+xml' }), arrayBuffer: async () => new ArrayBuffer(failure === 'empty' ? 0 : 65537) });
  const view = render(<PluginNavIcon ownerId="local" pluginId="org.example.tools" icon={{ path: 'a.svg' }} />);
  await act(async () => {});
  expect(view.container.querySelector('.bi-puzzle')).toBeInTheDocument();
  expect(view.container.querySelector('img')).toBeNull();
  expect(createURL).not.toHaveBeenCalled();
});

it('aborts stale requests when installation metadata changes', async () => {
  let resolve!: (value: unknown) => void;
  fetchMock.mockImplementationOnce(() => new Promise((r) => { resolve = r; }));
  const view = render(<PluginNavIcon ownerId="local" pluginId="org.example.tools" icon={{ path: 'a.svg' }} />);
  const signal = fetchMock.mock.calls[0][1].signal as AbortSignal;
  view.rerender(<PluginNavIcon ownerId="local" pluginId="org.example.tools" icon={{ bootstrap: 'journal-text' }} />);
  expect(signal.aborted).toBe(true);
  await act(async () => resolve({ ok: true, headers: new Headers({ 'Content-Type': 'image/svg+xml' }), arrayBuffer: async () => new ArrayBuffer(10) }));
  expect(createURL).not.toHaveBeenCalled();
  expect(view.container.querySelector('i')).toHaveClass('bi-journal-text');
});
