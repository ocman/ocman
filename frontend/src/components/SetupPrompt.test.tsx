// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { SetupPrompt } from './SetupPrompt';
import type { DoctorCheck, DoctorReport } from '../lib/api.types';

const mocks = vi.hoisted(() => ({ getDoctor: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { getDoctor: mocks.getDoctor } }));

const store = vi.hoisted(() => new Map<string, string>());
beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, String(v)); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => { store.clear(); },
  });
});
afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  store.clear();
});

function check(id: string, over: Partial<DoctorCheck> = {}): DoctorCheck {
  return { id, label: id, required: false, ok: true, detail: '', hint: '', ...over };
}

function report(...checks: DoctorCheck[]): DoctorReport {
  return { checks, logPath: '/tmp/ocman.log' };
}

function renderPrompt() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><SetupPrompt /></QueryClientProvider>);
}

describe('SetupPrompt', () => {
  it('renders nothing when every check is ok', async () => {
    mocks.getDoctor.mockResolvedValue(report(check('opencode', { required: true }), check('tmux')));
    renderPrompt();
    await waitFor(() => expect(mocks.getDoctor).toHaveBeenCalled());
    expect(screen.queryByTestId('setup-panel')).toBeNull();
    expect(screen.queryByTestId('setup-banner')).toBeNull();
  });

  it('blocks with a panel when a required check fails, and re-checks', async () => {
    mocks.getDoctor.mockResolvedValueOnce(report(
      check('opencode-db', { required: true, ok: false, label: 'OpenCode database', hint: 'Run OpenCode once' }),
      check('tmux', { ok: false }),
    ));
    renderPrompt();
    const panel = await screen.findByTestId('setup-panel');
    expect(panel.textContent).toContain('OpenCode database');
    expect(panel.textContent).toContain('Run OpenCode once');
    expect(panel.textContent).toContain('/tmp/ocman.log');
    expect(panel.textContent).not.toContain('tmux');

    mocks.getDoctor.mockResolvedValueOnce(report(check('opencode-db', { required: true })));
    fireEvent.click(screen.getByTestId('setup-recheck'));
    await waitFor(() => expect(screen.queryByTestId('setup-panel')).toBeNull());
    expect(mocks.getDoctor).toHaveBeenCalledTimes(2);
  });

  it('shows a banner for missing optional tools with unlocks, hint and PATH note', async () => {
    mocks.getDoctor.mockResolvedValue(report(
      check('tmux', { ok: false, hint: 'brew install tmux' }),
      check('login-shell-path', { ok: false }),
    ));
    renderPrompt();
    const banner = await screen.findByTestId('setup-banner');
    expect(banner.textContent).toContain('brew install tmux');
    expect(banner.textContent).toContain('unlocks launching managed OpenCode sessions');
    expect(banner.textContent).toContain('minimal PATH');
    expect(screen.queryByTestId('setup-panel')).toBeNull();
  });

  it('persists dismissal per failing set and re-shows for a newly missing tool', async () => {
    mocks.getDoctor.mockResolvedValue(report(check('tmux', { ok: false }), check('lsof', { ok: false })));
    const first = renderPrompt();
    await screen.findByTestId('setup-banner');
    fireEvent.click(screen.getByLabelText('Dismiss'));
    expect(screen.queryByTestId('setup-banner')).toBeNull();
    expect(store.get('ocman.setup-banner-dismissed')).toBe('lsof,tmux');
    first.unmount();

    const second = renderPrompt();
    await waitFor(() => expect(mocks.getDoctor).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId('setup-banner')).toBeNull();
    second.unmount();

    mocks.getDoctor.mockResolvedValue(report(
      check('tmux', { ok: false }), check('lsof', { ok: false }), check('whisper', { ok: false }),
    ));
    renderPrompt();
    expect(await screen.findByTestId('setup-banner')).toBeTruthy();
  });
});
