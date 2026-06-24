// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { useSaveStatus } from '../lib/useSaveStatus';
import { SaveStatus } from './SaveStatus';

function Harness({ run }: { run: () => Promise<unknown> }) {
  const { state, track } = useSaveStatus();
  return (
    <div>
      <button onClick={() => void track(run).catch(() => {})}>save</button>
      <SaveStatus state={state} />
    </div>
  );
}

describe('useSaveStatus', () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  it('shows spinner while saving, then check for 5s, then clears', async () => {
    let resolve!: () => void;
    const run = () => new Promise<void>((r) => { resolve = r; });
    render(<Harness run={run} />);

    await act(async () => { screen.getByText('save').click(); });
    expect(screen.getByTestId('save-status-spinner')).toBeTruthy();

    await act(async () => { resolve(); });
    expect(screen.getByTestId('save-status-saved')).toBeTruthy();

    await act(async () => { vi.advanceTimersByTime(5000); });
    expect(screen.queryByTestId('save-status-saved')).toBeNull();
  });

  it('shows error when the save rejects', async () => {
    const run = () => Promise.reject(new Error('boom'));
    render(<Harness run={run} />);
    await act(async () => { screen.getByText('save').click(); });
    expect(screen.getByTestId('save-status-error')).toBeTruthy();
  });
});
