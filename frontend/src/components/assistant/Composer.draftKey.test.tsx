// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { clearDraft, getDraft, saveDraft } from '../../lib/composerDraft';
import { api, BackendUnavailableError } from '../../lib/api';
import { Composer } from './Composer';

beforeEach(() => { clearDraft('new'); clearDraft('s1'); vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [], remotes: [] }); });
afterEach(() => { vi.restoreAllMocks(); });

// A new conversation has no session: its draft lives under its own key and
// the platform catalog is not fetched for a session that does not exist.
it('keys drafts by draftKey when there is no session', () => {
  const commands = vi.spyOn(api, 'commands');
  saveDraft('new', 'typed earlier');
  const view = render(<Composer isRunning={false} directory="/repo" newConversation draftKey="new" />);
  const input = screen.getByRole('textbox');
  expect(input).toHaveValue('typed earlier');
  fireEvent.input(input, { target: { value: 'typed now' } });
  view.unmount();
  expect(getDraft('new')).toBe('typed now');
  expect(getDraft('s1')).toBe('');
  expect(commands).not.toHaveBeenCalled();
});

it('falls back to the session id as draft key', () => {
  vi.spyOn(api, 'commands').mockResolvedValue([]);
  saveDraft('s1', 'session draft');
  render(<Composer isRunning={false} sessionId="s1" />);
  expect(screen.getByRole('textbox')).toHaveValue('session draft');
});

// Regression: a prompt in flight must never be parked as a draft, whether the
// composer unmounts (new conversation navigates away) or is re-pointed.
it('does not keep a submitted prompt as a draft when the composer unmounts mid-send', () => {
  const onSend = vi.fn(() => new Promise<void>(() => {}));
  const view = render(<Composer isRunning={false} directory="/repo" newConversation draftKey="new" onSend={onSend} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'ship it' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  expect(onSend).toHaveBeenCalled();
  view.unmount();
  expect(getDraft('new')).toBe('');
});

it('does not keep a submitted prompt as the old session draft when re-pointed mid-send', async () => {
  vi.spyOn(api, 'commands').mockResolvedValue([]);
  let resolve!: () => void;
  const onSend = vi.fn(() => new Promise<void>((r) => { resolve = r; }));
  saveDraft('s2', 'other draft');
  const view = render(<Composer isRunning={false} sessionId="s1" onSend={onSend} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'ship it' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  view.rerender(<Composer isRunning={false} sessionId="s2" onSend={onSend} />);
  expect(input).toHaveValue('other draft');
  await act(async () => { resolve(); });
  expect(getDraft('s1')).toBe('');
  expect(getDraft('s2')).toBe('other draft');
  expect(input).toHaveValue('other draft');
});

it('restores the draft when a send fails after unmount', async () => {
  let reject!: (e: Error) => void;
  const onSend = vi.fn(() => new Promise<void>((_, r) => { reject = r; }));
  const view = render(<Composer isRunning={false} directory="/repo" newConversation draftKey="new" onSend={onSend} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'ship it' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  view.unmount();
  await act(async () => { reject(new Error('boom')); });
  expect(getDraft('new')).toBe('ship it');
});

it('clears the old session draft when a backend retry lands after a re-point', async () => {
  vi.useFakeTimers();
  try {
    vi.spyOn(api, 'commands').mockResolvedValue([]);
    let calls = 0;
    const onSend = vi.fn(async () => { if (calls++ === 0) throw new BackendUnavailableError(); });
    const view = render(<Composer isRunning={false} sessionId="s1" onSend={onSend} />);
    const input = screen.getByRole('textbox');
    fireEvent.input(input, { target: { value: 'ship it' } });
    await act(async () => { fireEvent.keyDown(input, { key: 'Enter' }); });
    view.rerender(<Composer isRunning={false} sessionId="s2" onSend={onSend} />);
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    expect(onSend).toHaveBeenCalledTimes(2);
    expect(getDraft('s1')).toBe('');
  } finally {
    vi.useRealTimers();
  }
});

it('keeps a late-restored draft after returning to the session before the send fails', async () => {
  vi.spyOn(api, 'commands').mockResolvedValue([]);
  let reject!: (e: Error) => void;
  const onSend = vi.fn(() => new Promise<void>((_, r) => { reject = r; }));
  const view = render(<Composer isRunning={false} sessionId="s1" onSend={onSend} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'ship it' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  view.rerender(<Composer isRunning={false} sessionId="s2" onSend={onSend} />);
  view.rerender(<Composer isRunning={false} sessionId="s1" onSend={onSend} />);
  await act(async () => { reject(new Error('boom')); });
  expect(input).toHaveValue('ship it');
  view.rerender(<Composer isRunning={false} sessionId="s2" onSend={onSend} />);
  expect(getDraft('s1')).toBe('ship it');
});

it('keeps a late-restored new-conversation draft after a remount', async () => {
  let reject!: (e: Error) => void;
  const onSend = vi.fn(() => new Promise<void>((_, r) => { reject = r; }));
  const first = render(<Composer isRunning={false} directory="/repo" newConversation draftKey="new" onSend={onSend} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'ship it' } });
  fireEvent.keyDown(input, { key: 'Enter' });
  first.unmount();
  const second = render(<Composer isRunning={false} directory="/repo" newConversation draftKey="new" />);
  await act(async () => { reject(new Error('boom')); });
  second.unmount();
  expect(getDraft('new')).toBe('ship it');
});
