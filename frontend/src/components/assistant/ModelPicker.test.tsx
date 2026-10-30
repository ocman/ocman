// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
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

  it('lists a hidden lookup alias once and shows usage hints', () => {
    render(<ModelPicker open models={[]} modelEntries={[
      { provider: 'anthropic', model: 'opus', modelName: 'Opus', description: 'Most capable', isAvailable: true },
      { provider: 'anthropic', model: 'claude-opus-4-7', modelName: 'Opus', description: 'Most capable', isAvailable: true, hidden: true },
    ]} onSelect={vi.fn()} onClose={vi.fn()} />);
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(row('Opus').textContent).toContain('Most capable');
  });
});

describe('ModelPicker platforms', () => {
  beforeAll(() => {
    Element.prototype.scrollIntoView = vi.fn();
  });

  const mixed: SessionModelEntry[] = [
    { platform: 'opencode', platformName: 'OpenCode', provider: 'anthropic', model: 'sonnet', modelName: 'Sonnet' },
    { platform: 'claudecode', platformName: 'Claude Code', provider: 'anthropic', model: 'sonnet', modelName: 'Sonnet' },
  ];

  it('filters by platform, preserves search across tabs, and selects the qualified model', async () => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    render(<ModelPicker open models={[]} modelEntries={mixed} onSelect={onSelect} onClose={vi.fn()} />);
    expect(screen.getByRole('tab', { name: 'All' })).toHaveAttribute('aria-selected', 'true');
    for (const [name, platform] of [['OpenCode', 'opencode'], ['Claude Code', 'claudecode']]) {
      expect(screen.getByRole('tab', { name }).querySelector('img'))
        .toHaveAttribute('src', `/platform-${platform}.svg`);
    }
    expect(screen.getByRole('tab', { name: 'All' }).querySelector('img')).toBeNull();
    for (const badge of screen.getAllByTestId('model-platform-badge')) {
      expect(badge.querySelector('img')).toHaveAttribute('alt', '');
    }
    expect(screen.getAllByRole('option')).toHaveLength(2);
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'Sonnet' } });
    await user.click(screen.getByRole('tab', { name: 'Claude Code' }));
    expect(screen.getAllByRole('option')).toHaveLength(1);
    expect(screen.getByRole('combobox')).toHaveValue('Sonnet');
    await user.click(screen.getByRole('tab', { name: 'OpenCode' }));
    expect(screen.getAllByRole('option')).toHaveLength(1);
    await user.click(screen.getByRole('tab', { name: 'All' }));
    expect(screen.getAllByRole('option')).toHaveLength(2);
    await user.click(screen.getByRole('tab', { name: 'Claude Code' }));
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(onSelect).toHaveBeenCalledWith('claudecode:anthropic/sonnet');
  });

  it.each([
    ['ArrowRight', ['OpenCode', 'Claude Code', 'All']],
    ['ArrowLeft', ['Claude Code', 'OpenCode', 'All']],
  ] as const)('cycles platform tabs with %s without moving focus or clearing search', async (key, names) => {
    const user = userEvent.setup();
    const onSelect = vi.fn();
    const onClose = vi.fn();
    render(<ModelPicker open models={[]} modelEntries={mixed} onSelect={onSelect} onClose={onClose} />);
    const input = screen.getByRole('combobox');
    await user.click(input);
    await user.type(input, 'Sonnet');
    for (const name of names) {
      await user.keyboard(`{${key}}`);
      expect(screen.getByRole('tab', { name })).toHaveAttribute('aria-selected', 'true');
      expect(input).toHaveFocus();
      expect(input).toHaveValue('Sonnet');
      expect(screen.getAllByRole('option')).toHaveLength(name === 'All' ? 2 : 1);
    }
    expect(onSelect).not.toHaveBeenCalled();
    expect(onClose).not.toHaveBeenCalled();
    await user.keyboard('{ArrowLeft}{Enter}');
    expect(onSelect).toHaveBeenCalledWith('claudecode:anthropic/sonnet');
  });

  it('resets the highlighted row when switching tabs, even with no search results', () => {
    render(<ModelPicker open models={[]} modelEntries={mixed} onSelect={vi.fn()} onClose={vi.fn()} />);
    const input = screen.getByRole('combobox');
    fireEvent.keyDown(input, { key: 'ArrowDown' });
    fireEvent.keyDown(input, { key: 'ArrowRight' });
    fireEvent.keyDown(input, { key: 'ArrowLeft' });
    expect(screen.getAllByRole('option')[0]).toHaveAttribute('aria-selected', 'true');
    fireEvent.change(input, { target: { value: 'missing-model' } });
    fireEvent.keyDown(input, { key: 'ArrowLeft' });
    expect(screen.getByRole('tab', { name: 'Claude Code' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText('No models found')).toBeInTheDocument();
  });

  it.each(['ArrowLeft', 'ArrowRight'])('leaves %s unhandled without platform tabs', (key) => {
    render(<ModelPicker open models={['anthropic/sonnet']} onSelect={vi.fn()} onClose={vi.fn()} />);
    expect(fireEvent.keyDown(screen.getByRole('combobox'), { key })).toBe(true);
  });

  it.each([
    { models: ['anthropic/sonnet'] },
    { models: [], modelEntries: [mixed[0]] },
    { models: [], modelEntries: [mixed[1]] },
    { models: [], modelEntries: [mixed[0], { ...mixed[1], hidden: true }] },
  ])('hides tabs without multiple visible platforms: %j', (props) => {
    render(<ModelPicker open {...props} onSelect={vi.fn()} onClose={vi.fn()} />);
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
  });

  it('falls back to all models if the selected platform disappears', async () => {
    const user = userEvent.setup();
    const props = { open: true, models: [], onSelect: vi.fn(), onClose: vi.fn() };
    const view = render(<ModelPicker {...props} modelEntries={mixed} />);
    await user.click(screen.getByRole('tab', { name: 'Claude Code' }));
    fireEvent.change(screen.getByRole('combobox'), { target: { value: 'missing-model' } });
    expect(screen.getByText('No models found')).toBeInTheDocument();
    fireEvent.change(screen.getByRole('combobox'), { target: { value: '' } });
    view.rerender(<ModelPicker {...props} modelEntries={[mixed[0]]} />);
    expect(screen.queryByRole('tablist')).not.toBeInTheDocument();
    expect(screen.getAllByRole('option')).toHaveLength(1);
    fireEvent.keyDown(screen.getByRole('combobox'), { key: 'Enter' });
    expect(props.onSelect).toHaveBeenCalledWith('anthropic/sonnet');
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
