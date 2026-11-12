// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { Composer } from './Composer';
import { api } from '../../lib/api';
import { canAutomaticallyReload } from '../../lib/automaticReload';
import { getDraft, resetDraftTextsForTests } from '../../lib/composerDraft';
import { resetDraftPayloadsForTests } from '../../lib/pendingDraftPayloads';
import { installPreloadRecovery } from '../../lib/preloadRecovery';

const a = 'a'.repeat(64), b = 'b'.repeat(64), c = 'c'.repeat(64);
let stop: (() => void) | undefined;
const failImport = () => window.dispatchEvent(new Event('vite:preloadError', { cancelable: true }));
const image = () => new File(['image'], 'image.png', { type: 'image/png' });
function pasteImage() {
  fireEvent.paste(screen.getByRole('textbox'), {
    clipboardData: { items: [{ type: 'image/png', getAsFile: image }] },
  });
}
beforeEach(() => {
  sessionStorage.clear(); resetDraftTextsForTests(); resetDraftPayloadsForTests();
  vi.spyOn(api, 'commands').mockResolvedValue([]);
  vi.spyOn(api, 'capabilities').mockResolvedValue({ platforms: [] });
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [], remotes: [] });
});
afterEach(() => { stop?.(); vi.restoreAllMocks(); vi.unstubAllGlobals(); vi.useRealTimers(); });

it('preserves an existing-session image when a later deployment fails after earlier recovery', async () => {
  sessionStorage.setItem('ocman:preload-reloaded', '1');
  let deployed = b;
  vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ hash: deployed }))));
  const reload = vi.fn();
  stop = installPreloadRecovery(a, reload);
  failImport();
  await waitFor(() => expect(reload).toHaveBeenCalledTimes(1));
  stop();
  deployed = c;
  stop = installPreloadRecovery(b, reload);
  render(<Composer sessionId="existing" isRunning={false} />);
  pasteImage();
  const remove = await screen.findByRole('button', { name: 'Remove image attachment 1' });
  expect(getDraft('existing')).toBe('');
  await act(async () => { failImport(); });
  expect(reload).toHaveBeenCalledTimes(1);
  expect(remove).toBeInTheDocument();
  expect(canAutomaticallyReload()).toBe(false);
  fireEvent.click(remove);
  expect(canAutomaticallyReload()).toBe(true);
});

it('blocks recovery during an existing-session image read and releases on unmount', () => {
  vi.spyOn(FileReader.prototype, 'readAsDataURL').mockImplementation(() => {});
  const view = render(<Composer sessionId="existing" isRunning={false} />);
  pasteImage();
  expect(screen.queryByRole('button', { name: 'Remove image attachment 1' })).not.toBeInTheDocument();
  expect(canAutomaticallyReload()).toBe(false);
  view.unmount();
  expect(canAutomaticallyReload()).toBe(true);
});

it('rechecks a local pending read accepted while the deployment probe was in flight', async () => {
  vi.spyOn(FileReader.prototype, 'readAsDataURL').mockImplementation(() => {});
  let respond!: (response: Response) => void;
  vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>(resolve => { respond = resolve; })));
  const reload = vi.fn();
  render(<Composer sessionId="existing" isRunning={false} />);
  stop = installPreloadRecovery(b, reload);
  failImport();
  expect(fetch).toHaveBeenCalledTimes(1);
  pasteImage();
  await act(async () => { respond(new Response(JSON.stringify({ hash: c }))); });
  expect(reload).not.toHaveBeenCalled();
  expect(canAutomaticallyReload()).toBe(false);
});

it('protects live text before the 300ms autosave, then releases when input is emptied', () => {
  vi.useFakeTimers();
  render(<Composer sessionId="existing" isRunning={false} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'not saved yet' } });
  expect(getDraft('existing')).toBe('');
  expect(canAutomaticallyReload()).toBe(false);
  fireEvent.input(input, { target: { value: '' } });
  expect(canAutomaticallyReload()).toBe(true);
});

it('releases the existing-session attachment guard after sending its image', async () => {
  const send = vi.fn();
  render(<Composer sessionId="existing" isRunning={false} onSend={send} />);
  pasteImage();
  await screen.findByRole('button', { name: 'Remove image attachment 1' });
  expect(canAutomaticallyReload()).toBe(false);
  await act(async () => { fireEvent.keyDown(screen.getByRole('textbox'), { key: 'Enter' }); });
  expect(send).toHaveBeenCalledWith('', [expect.objectContaining({ mime: 'image/png' })], false, undefined);
  expect(canAutomaticallyReload()).toBe(true);
});
