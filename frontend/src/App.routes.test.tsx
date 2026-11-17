// @vitest-environment jsdom
import { it, expect } from 'vitest';
import { render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { AppRoutes } from './App';

it.each(['/does-not-exist', '/activity', '/project'])('shows a way back on the unknown route %s', async (path) => {
  render(<MemoryRouter initialEntries={[path]}><AppRoutes /></MemoryRouter>);
  expect(await screen.findByRole('heading', { name: 'Page not found' })).toBeInTheDocument();
  expect(screen.getByRole('link', { name: /home/i })).toHaveAttribute('href', '/');
});
