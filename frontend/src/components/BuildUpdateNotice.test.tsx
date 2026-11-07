// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { BuildUpdateNotice } from './BuildUpdateNotice';

beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal('fetch', vi.fn().mockImplementation(() => Promise.resolve(new Response(JSON.stringify({ hash: 'b'.repeat(64) })))));
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
