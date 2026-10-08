// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { expect, it } from 'vitest';
import { RouteButton } from './Control';

it('styles a real route link and preserves client-side navigation with its owner query', async () => {
  const user = userEvent.setup();
  render(<MemoryRouter><RouteButton to="/project/%2Frepo?remoteId=B" size="small">Back to project</RouteButton><Routes><Route path="/" element={null} /><Route path="/project/:dir" element={<p>Project destination</p>} /></Routes></MemoryRouter>);
  const link = screen.getByRole('link', { name: 'Back to project' });
  expect(link).toHaveClass('oc-button', 'oc-button--small');
  expect(link).toHaveAttribute('href', '/project/%2Frepo?remoteId=B');
  await user.click(link);
  expect(screen.getByText('Project destination')).toBeInTheDocument();
});
