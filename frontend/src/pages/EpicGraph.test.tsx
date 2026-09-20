// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { describe, expect, it } from 'vitest';
import { EpicGraph } from './EpicGraph';

describe('workflow graph', () => {
  it('expands the implementation phase to show its planned tasks', async () => {
    const user = userEvent.setup();
    render(<EpicGraph issues={[
      { id: 'phase', epicId: 'epic', project: '/repo', kind: 'phase', title: 'Implement', status: 'open' },
      { id: 'task', epicId: 'epic', project: '/repo', kind: 'implementation', parentId: 'phase', title: 'Planned task', status: 'open' },
    ]} />);
    expect(screen.queryByText('Planned task')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Show tasks for Implement' }));
    expect(screen.getByText('Planned task')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Hide tasks for Implement' }));
    expect(screen.queryByText('Planned task')).not.toBeInTheDocument();
  });
});
