// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { expect, it } from 'vitest';
import { RouteTabs } from './RouteTabs';

function Where() { const l = useLocation(); return <output>{l.pathname + l.search}</output>; }

it('renders route links with aria-current and keeps search parameters', () => {
  render(<MemoryRouter initialEntries={['/catalog/items?ownerId=machine&q=lamp']}>
    <Routes><Route path="/catalog/*" element={<>
      <RouteTabs label="Catalog views" tabs={[{ to: '/catalog/items', label: 'Items' }, { to: '/catalog/archive', label: 'Archive' }]} />
      <Where />
    </>} /></Routes>
  </MemoryRouter>);
  const nav = screen.getByRole('navigation', { name: 'Catalog views' });
  expect(nav).toHaveClass('nav-tabs');
  expect(screen.getByRole('link', { name: 'Items' })).toHaveAttribute('aria-current', 'page');
  const archive = screen.getByRole('link', { name: 'Archive' });
  expect(archive).not.toHaveAttribute('aria-current');
  expect(archive).toHaveAttribute('href', '/catalog/archive?ownerId=machine&q=lamp');
  fireEvent.click(archive);
  expect(screen.getByRole('status')).toHaveTextContent('/catalog/archive?ownerId=machine&q=lamp');
  expect(screen.getByRole('link', { name: 'Archive' })).toHaveAttribute('aria-current', 'page');
});
