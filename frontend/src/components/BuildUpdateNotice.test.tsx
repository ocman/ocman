// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { BuildUpdateNotice } from './BuildUpdateNotice';
import { getDraft, resetDraftTextsForTests, saveDraft } from '../lib/composerDraft';

beforeEach(() => {
  resetDraftTextsForTests();
  sessionStorage.clear();
  vi.useFakeTimers();
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ hash: 'b'.repeat(64) })))));
});

it('does not let the update countdown discard a composer draft', async () => {
  saveDraft('session', 'unsent prompt');
  const reload = vi.fn();
  render(<BuildUpdateNotice buildHash={'a'.repeat(64)} reload={reload} />);
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(reload).not.toHaveBeenCalled();
  expect(getDraft('session')).toBe('unsent prompt');
  expect(screen.getByText(/Automatic refresh paused/)).toBeInTheDocument();
});

it('cannot bypass a preload recovery budget when a reload serves stale HTML', async () => {
  sessionStorage.setItem('ocman:preload-recovery:v1', JSON.stringify([
    { from: 'a'.repeat(64), to: 'b'.repeat(64), at: Date.now() },
  ]));
  const reload = vi.fn();
  render(<BuildUpdateNotice buildHash={'a'.repeat(64)} reload={reload} />);
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(reload).not.toHaveBeenCalled();
  expect(screen.getByText(/Automatic refresh paused/)).toBeInTheDocument();
});
afterEach(() => { cleanup(); vi.useRealTimers(); vi.unstubAllGlobals(); });

it('offers refresh now and automatically refreshes once after 60 seconds', async () => {
  const reload = vi.fn();
  render(<BuildUpdateNotice buildHash={'a'.repeat(64)} reload={reload} />);
  await act(() => vi.advanceTimersByTimeAsync(0));
  expect(screen.getByText('Update available')).toBeInTheDocument();
  expect(screen.getByText(/in 60 seconds/)).toBeInTheDocument();
  await act(() => vi.advanceTimersByTimeAsync(59_000));
  expect(reload).not.toHaveBeenCalled();
  expect(screen.getByText(/in 1 second\./)).toBeInTheDocument();
  await act(() => vi.advanceTimersByTimeAsync(1000));
  expect(reload).toHaveBeenCalledTimes(1);
  await act(() => vi.advanceTimersByTimeAsync(5000));
  expect(reload).toHaveBeenCalledTimes(1);
});

it('refreshes immediately when requested and cleans up on unmount', async () => {
  const reload = vi.fn();
  const view = render(<BuildUpdateNotice buildHash={'a'.repeat(64)} reload={reload} />);
  await act(() => vi.advanceTimersByTimeAsync(0));
  fireEvent.click(screen.getByRole('button', { name: 'Refresh now' }));
  expect(reload).toHaveBeenCalledOnce();
  view.unmount();
  await act(() => vi.advanceTimersByTimeAsync(60_000));
  expect(reload).toHaveBeenCalledOnce();
});
