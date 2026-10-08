// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { expect, it } from 'vitest';
import { ProjectShell } from './ProjectShell';
import userEvent from '@testing-library/user-event';

function History() {
  const location = useLocation();
  const navigate = useNavigate();
  return <><output>{location.pathname + location.search}</output><button onClick={() => navigate(-1)}>Back</button><button onClick={() => navigate(1)}>Forward</button></>;
}

it.each(['local', 'B'])('preserves %s ownership and session filters through tabs and history', async (owner) => {
  const user = userEvent.setup();
  render(<MemoryRouter initialEntries={[`/project/%2Frepo?remoteId=${owner}&q=fix&t=0&a=1`]}>
    <div id="header-actions-slot" />
    <Routes>
      <Route path="/project/:dir" element={<ProjectShell view="sessions">Session rows</ProjectShell>} />
      <Route path="/project/:dir/worktrees" element={<ProjectShell view="worktrees">Worktree rows</ProjectShell>} />
      <Route path="/project/:dir/settings" element={<ProjectShell view="settings">Settings rows</ProjectShell>} />
    </Routes>
    <History />
  </MemoryRouter>);
  await user.click(screen.getByRole('tab', { name: 'Worktrees' }));
  expect(screen.getByRole('tabpanel')).toHaveTextContent('Worktree rows');
  expect(screen.getByRole('status')).toHaveTextContent(`/project/%2Frepo/worktrees?remoteId=${owner}&q=fix&t=0&a=1`);
  await user.click(screen.getByRole('tab', { name: 'Settings' }));
  expect(screen.getByRole('tabpanel')).toHaveTextContent('Settings rows');
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  expect(screen.getByRole('tab', { name: 'Worktrees' })).toHaveAttribute('aria-selected', 'true');
  fireEvent.click(screen.getByRole('button', { name: 'Back' }));
  expect(screen.getByRole('tab', { name: 'Sessions' })).toHaveAttribute('aria-selected', 'true');
  fireEvent.click(screen.getByRole('button', { name: 'Forward' }));
  expect(screen.getByRole('tab', { name: 'Worktrees' })).toHaveAttribute('aria-selected', 'true');
});
