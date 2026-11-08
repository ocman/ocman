// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { beforeEach, expect, it, vi } from 'vitest';
import { postJSON } from '../lib/api.requests';
import { useShortcutDispatcher, useShortcutRegistry } from '../lib/shortcutRegistry';
import { OneOffSessionAction } from './OneOffSessionAction';

vi.mock('../lib/api.requests', () => ({ postJSON: vi.fn() }));
const post = vi.mocked(postJSON);

function Harness({ onNavigate, shortcutOnly }: { onNavigate?: () => void; shortcutOnly?: boolean }) {
  useShortcutDispatcher();
  const location = useLocation();
  return <><OneOffSessionAction onNavigate={onNavigate} shortcutOnly={shortcutOnly} /><output data-testid="route">{location.pathname}{location.search}</output><textarea aria-label="Prompt" /></>;
}

beforeEach(() => { vi.resetAllMocks(); });

it('keeps Option+O available without a navigation action', async () => {
  post.mockResolvedValue({ directory: '/tmp/ocman-one-off-hidden' });
  render(<MemoryRouter><Harness shortcutOnly /></MemoryRouter>);
  expect(screen.queryByRole('button', { name: 'New one-off session' })).not.toBeInTheDocument();
  fireEvent.keyDown(window, { code: 'KeyO', altKey: true });
  await waitFor(() => expect(screen.getByTestId('route').textContent).toContain('/session/ocm_'));
});

it('opens a normal draft in a new local workspace without choosing a project', async () => {
  post.mockResolvedValue({ directory: '/tmp/ocman-one-off-first' });
  const onNavigate = vi.fn();
  render(<MemoryRouter><Harness onNavigate={onNavigate} /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'New one-off session' }));
  await waitFor(() => expect(screen.getByTestId('route').textContent).toContain('/session/ocm_'));
  expect(post).toHaveBeenCalledWith('/api/sessions/one-off', { remoteId: 'local' });
  const url = new URL(screen.getByTestId('route').textContent!, 'http://localhost');
  expect(url.searchParams.get('dir')).toBe('/tmp/ocman-one-off-first');
  expect(url.searchParams.get('draftId')).toBe(url.pathname.split('/').pop());
  expect(onNavigate).toHaveBeenCalledOnce();
});

it('handles Mac Option+O inside the composer and keeps explicit ownership', async () => {
  post.mockResolvedValue({ directory: '/remote/tmp/one-off' });
  render(<MemoryRouter initialEntries={['/sessions?remoteId=box']}><Harness /></MemoryRouter>);
  const input = screen.getByRole('textbox', { name: 'Prompt' });
  input.focus();
  expect(fireEvent.keyDown(input, { code: 'KeyO', key: 'ø', altKey: true })).toBe(false);
  await waitFor(() => expect(screen.getByTestId('route').textContent).toContain('/session/ocm_'));
  expect(screen.getByTestId('route').textContent).toContain('remoteId=box');
  expect(post).toHaveBeenCalledWith('/api/sessions/one-off', { remoteId: 'box' });
  expect(input).toHaveValue('');
});

it('prevents duplicate allocation while the request is in flight', async () => {
  let resolve!: (value: unknown) => void;
  post.mockImplementation(() => new Promise((done) => { resolve = done; }));
  render(<MemoryRouter><Harness /></MemoryRouter>);
  fireEvent.keyDown(window, { code: 'KeyO', altKey: true });
  fireEvent.keyDown(window, { code: 'KeyO', altKey: true });
  expect(post).toHaveBeenCalledOnce();
  expect(screen.getByRole('button', { name: 'New one-off session' })).toBeDisabled();
  await act(async () => { resolve({ directory: '/tmp/one-off' }); });
});

it('shows allocation failures, then allows retry without navigating on error', async () => {
  post.mockRejectedValueOnce(new Error('Machine is disconnected')).mockResolvedValueOnce({ directory: '/tmp/retry' });
  const { container } = render(<MemoryRouter><Harness /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'New one-off session' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Machine is disconnected');
  expect(container).not.toContainElement(screen.getByRole('dialog'));
  expect(screen.getByTestId('route')).toHaveTextContent('/');
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  expect(post).toHaveBeenCalledTimes(2);
});

it('allows cancelling an allocation error', async () => {
  post.mockRejectedValue('Allocation failed');
  render(<MemoryRouter><Harness /></MemoryRouter>);
  fireEvent.click(screen.getByRole('button', { name: 'New one-off session' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Allocation failed');
  fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it('supports a phone action without registering a second global shortcut', async () => {
  post.mockResolvedValue({ directory: '/tmp/phone' });
  const onNavigate = vi.fn();
  render(<MemoryRouter><OneOffSessionAction registerShortcut={false} onNavigate={onNavigate} /></MemoryRouter>);
  expect(useShortcutRegistry.getState().shortcuts.has('site.one-off-session')).toBe(false);
  fireEvent.click(screen.getByRole('button', { name: 'New one-off session' }));
  await waitFor(() => expect(onNavigate).toHaveBeenCalledOnce());
});
