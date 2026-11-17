// @vitest-environment jsdom
import { render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, describe, expect, it, vi } from 'vitest';

const isPhone = vi.hoisted(() => ({ value: true }));
const dashboard = vi.hoisted(() => ({ dirScope: '' }));
vi.mock('../../lib/useIsPhone', () => ({ useIsPhone: () => isPhone.value }));
vi.mock('../../components/ProjectScopePicker', () => ({ ProjectScopePicker: () => <div>project scope</div> }));
vi.mock('./context', () => ({ useDashboard: () => ({ projects: [], dirScope: dashboard.dirScope, setDirScope: vi.fn() }) }));

import { AnalyticsFilters } from './AnalyticsFilters';

const agentOptions = [{ value: '', label: 'All agents' }, { value: 'build', label: 'build' }];

describe('AnalyticsFilters', () => {
  beforeEach(() => { isPhone.value = true; dashboard.dirScope = ''; });

  it('shows the fields inline on desktop', () => {
    isPhone.value = false;
    render(<AnalyticsFilters days={30} onDaysChange={vi.fn()} agent="" onAgentChange={vi.fn()} agentOptions={agentOptions} />);
    expect(screen.getByRole('combobox', { name: 'Agent' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Filters/ })).not.toBeInTheDocument();
  });

  it('collapses phone filters into a summary that opens a sheet', async () => {
    const user = userEvent.setup();
    render(<AnalyticsFilters days={30} onDaysChange={vi.fn()} agent="" onAgentChange={vi.fn()} agentOptions={agentOptions} />);
    expect(screen.getByText('All projects · 30 days')).toBeInTheDocument();
    expect(screen.queryByRole('combobox', { name: 'Agent' })).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Filters' }));
    const sheet = screen.getByRole('dialog', { name: 'Filters' });
    expect(within(sheet).getByRole('combobox', { name: 'Agent' })).toBeInTheDocument();
    await user.click(within(sheet).getByRole('button', { name: 'Done' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('summarises and counts active filters', () => {
    dashboard.dirScope = '/home/u/src/proj';
    render(<AnalyticsFilters days={7} onDaysChange={vi.fn()} agent="build" onAgentChange={vi.fn()} agentOptions={agentOptions} model="x/unlisted" onModelChange={vi.fn()} />);
    expect(screen.getByText('src/proj · build · x/unlisted · 7 days')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Filters (3)' })).toBeInTheDocument();
  });
});
