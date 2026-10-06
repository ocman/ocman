// @vitest-environment jsdom
import { useState } from 'react';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { DashboardToolbar } from './DashboardToolbar';

const project = { directory: '/work/myapp', sessionCount: 1, messageCount: 2, totalTokensIn: 0, totalTokensOut: 0, lastUsed: 0 };

it('uses shared controls and preserves search, project selection and the create action', async () => {
  const user = userEvent.setup();
  const onAction = vi.fn();
  const setScope = vi.fn();
  function Fixture() {
    const [search, setSearch] = useState('');
    return <DashboardToolbar projects={[project]} dirScope="" setDirScope={setScope} search={search} setSearch={setSearch}
      searchLabel="Search sessions" actionIcon="bi-plus-lg" actionLabel="New session" actionTitle="Start a new session" onAction={onAction} />;
  }
  render(<Fixture />);
  const search = screen.getByRole('searchbox', { name: 'Search sessions' });
  expect(search).toHaveClass('oc-field');
  await user.type(search, 'authentication');
  expect(search).toHaveValue('authentication');
  await user.click(screen.getByRole('combobox', { name: 'Project scope' }));
  await user.click(screen.getByRole('option', { name: /myapp/ }));
  expect(setScope).toHaveBeenCalledWith('/work/myapp');
  const action = screen.getByRole('button', { name: 'New session' });
  expect(action).toHaveClass('oc-button', 'oc-button--accent');
  expect(action).toHaveAttribute('type', 'button');
  expect(action).toHaveAttribute('title', 'Start a new session');
  await user.click(action);
  expect(onAction).toHaveBeenCalledOnce();
});

it('disables an empty project picker without disabling search or session creation', () => {
  render(<DashboardToolbar projects={[]} dirScope="" setDirScope={() => {}} search="" setSearch={() => {}}
    searchLabel="Search sessions" actionIcon="bi-plus-lg" actionLabel="New session" actionTitle="Start a new session" onAction={() => {}} />);
  expect(screen.getByRole('combobox', { name: 'Project scope' })).toBeDisabled();
  expect(screen.getByRole('searchbox', { name: 'Search sessions' })).toBeEnabled();
  expect(screen.getByRole('button', { name: 'New session' })).toBeEnabled();
});
