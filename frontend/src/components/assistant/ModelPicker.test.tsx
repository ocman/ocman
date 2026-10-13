// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeAll, beforeEach, describe, expect, it, vi } from 'vitest';
import type { SessionModelEntry } from '../../lib/api';
import { ModelPicker } from './ModelPicker';

describe('ModelPicker cooldown', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-01-01T00:00:00Z'));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  const entries: SessionModelEntry[] = [
    { provider: 'a', model: 'x', isAvailable: true, cooldownUntil: '2026-01-01T00:01:30Z' },
    { provider: 'b', model: 'y', isAvailable: true },
  ];

  function renderPicker() {
    const onSelect = vi.fn();
    render(<ModelPicker open models={[]} modelEntries={entries} onSelect={onSelect} onClose={vi.fn()} />);
    return { onSelect };
  }

  const row = (name: string) => screen.getAllByRole('option').find((o) => o.textContent?.includes(name))!;

  it('marks only the cooled-down model and keeps it selectable', () => {
    const { onSelect } = renderPicker();
    expect(row('x').textContent).toContain('unavailable · 1m 30s');
    expect(row('y').textContent).not.toContain('unavailable');
    expect(row('y').querySelector('.oc-model-picker-badge--cooldown')).toBeNull();
    fireEvent.click(row('x'));
    expect(onSelect).toHaveBeenCalledWith('a/x');
  });

  it('counts down and clears the mark once the cooldown expires', () => {
    renderPicker();
    act(() => { vi.advanceTimersByTime(30_000); });
    expect(row('x').textContent).toContain('unavailable · 1m 0s');
    act(() => { vi.advanceTimersByTime(61_000); });
    expect(row('x').textContent).not.toContain('unavailable');
  });

  it('explains disconnected providers without calling models archived', () => {
    render(<ModelPicker open models={[]} modelEntries={[
      { provider: 'a', model: 'disconnected', isAvailable: false },
      { provider: 'a', model: 'unknown' },
    ]} onSelect={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByText('Archived')).not.toBeInTheDocument();
    expect(screen.queryByText('archived')).not.toBeInTheDocument();
    expect(row('disconnected').textContent).toContain('provider disconnected');
    expect(row('unknown').textContent).not.toContain('provider disconnected');
    expect(screen.getByTitle('Provider is not connected on this session\'s machine. Connect it or choose another provider.')).toBeInTheDocument();
  });
});

describe('ModelPicker platforms', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  it('badges each row with its platform only when the list spans platforms, and selects the qualified model', () => {
    const onSelect = vi.fn();
    const onToggleFavorite = vi.fn();
    const mixed: SessionModelEntry[] = [
      { platform: 'opencode', platformName: 'OpenCode', provider: 'anthropic', providerName: 'Anthropic', model: 'claude-sonnet', isAvailable: true },
      { platform: 'claudecode', platformName: 'Claude Code', provider: 'anthropic', providerName: 'Anthropic', model: 'sonnet', isAvailable: true },
    ];
    const view = render(<ModelPicker open models={[]} modelEntries={mixed} onSelect={onSelect} onToggleFavorite={onToggleFavorite} onClose={vi.fn()} />);
    expect(screen.getAllByTestId('model-platform-badge').map((b) => b.textContent)).toEqual(['OpenCode', 'Claude Code']);
    const claude = screen.getAllByRole('option').find((o) => o.textContent?.includes('Claude Code'))!;
    fireEvent.click(claude.querySelector('button')!);
    expect(onToggleFavorite).toHaveBeenCalledWith('anthropic', 'sonnet', true, 'claudecode');
    fireEvent.click(claude);
    expect(onSelect).toHaveBeenCalledWith('claudecode:anthropic/sonnet');
    view.unmount();
    render(<ModelPicker open models={[]} modelEntries={[mixed[1]]} onSelect={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByTestId('model-platform-badge')).not.toBeInTheDocument();
  });
});
