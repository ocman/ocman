// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { clearDraft, discardDraft, getDraft, getDraftVersion, saveDraft } from '../../lib/composerDraft';
import { api, BackendUnavailableError } from '../../lib/api';
import { Composer } from './Composer';
import { transact } from '../../lib/draftDb';

beforeEach(() => { clearDraft('new'); clearDraft('s1'); vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [], remotes: [] }); });
afterEach(() => { vi.restoreAllMocks(); });

it('keeps typed text and reports a failed autosave in the mounted composer', async () => {
  saveDraft('write-error-ui', 'stored text');
  await transact(['texts'], 'readonly', (tx) => tx.get('texts', 'write-error-ui'));
  render(<Composer isRunning={false} newConversation draftKey="write-error-ui" />);
  const fail = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(() => { throw new DOMException('quota', 'QuotaExceededError'); });
  try {
    fireEvent.input(screen.getByRole('textbox'), { target: { value: 'latest live text' } });
    expect(await screen.findByRole('alert')).toHaveTextContent('Draft not saved');
    expect(screen.getByRole('textbox')).toHaveValue('latest live text');
  } finally { fail.mockRestore(); }
  fireEvent.input(screen.getByRole('textbox'), { target: { value: 'latest live text!' } });
  await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
  expect(getDraft('write-error-ui')).toBe('latest live text!');
});

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

it('adopts an external clear revision for deliberate new edits', () => {
  saveDraft('new', 'old text');
  const view = render(<Composer isRunning={false} newConversation draftKey="new" />);
  discardDraft('new');
  fireEvent.input(screen.getByRole('textbox'), { target: { value: 'fresh deliberate edit' } });
  view.unmount();
  expect(getDraft('new')).toBe('fresh deliberate edit');
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

it('mirrors another tab without autosaving or advancing the discard fence', async () => {
  vi.useFakeTimers();
  try {
    render(<Composer isRunning={false} newConversation draftKey="synced" shellExec />);
    const input = screen.getByRole('textbox');
    act(() => saveDraft('synced', '!ls from the peer'));
    expect(input).toHaveValue('!ls from the peer');
    // Presentation follows the synchronized text: shell mode is on.
    expect(input.closest('.oc-composer')).toHaveStyle({ borderLeftColor: '#f38ba8' });
    const revision = getDraftVersion('synced');
    const write = vi.spyOn(IDBObjectStore.prototype, 'put');
    act(() => clearDraft('synced'));
    expect(input).toHaveValue('');
    await act(async () => { vi.advanceTimersByTime(500); });
    // Neither the synchronized text nor its clear is written back as this tab's edit.
    expect(write).not.toHaveBeenCalled();
    expect(getDraftVersion('synced')).toBe(revision);
  } finally { vi.useRealTimers(); }
});
