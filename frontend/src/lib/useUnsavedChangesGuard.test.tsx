// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { Link, MemoryRouter, Route, Routes, useLocation, useNavigationType } from 'react-router-dom';
import { expect, it, vi } from 'vitest';
import { ConfirmProvider } from '../components/ConfirmDialog';
import { useUnsavedChangesGuard } from './useUnsavedChangesGuard';

let discarded: boolean | undefined;

function Editor() {
  const [dirty, setDirty] = useState(false);
  const { confirmDiscard } = useUnsavedChangesGuard(dirty);
  return <>
    <button onClick={() => setDirty(true)}>Edit</button>
    <button onClick={() => void confirmDiscard().then((ok) => { discarded = ok; })}>Cancel editing</button>
    <Link to="/elsewhere?tab=b">Elsewhere</Link>
    <Link to="/editor">Same page</Link>
  </>;
}

function Where() { return <output>{useLocation().pathname + useLocation().search}</output>; }
function NavigationType() { return <p data-testid="navigation-type">{useNavigationType()}</p>; }
const onNavigate = vi.fn();

function mount() {
  discarded = undefined;
  onNavigate.mockClear();
  render(<MemoryRouter initialEntries={['/editor']}><ConfirmProvider>
    <nav><Link to="/sessions">Sessions</Link><Link to="/inbox" replace onClick={onNavigate}>Inbox</Link></nav>
    <NavigationType />
    <Routes><Route path="/editor" element={<Editor />} /><Route path="*" element={<p>Other page</p>} /></Routes>
    <Where />
  </ConfirmProvider></MemoryRouter>);
}

it('lets clean editors navigate freely', async () => {
  mount();
  fireEvent.click(screen.getByRole('link', { name: 'Sessions' }));
  expect(await screen.findByText('Other page')).toBeInTheDocument();
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('asks before following any in-app link while dirty, including main navigation', async () => {
  mount();
  fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
  fireEvent.click(screen.getByRole('link', { name: 'Sessions' }));
  expect(screen.getByRole('dialog', { name: 'Discard unsaved changes?' })).toBeInTheDocument();
  expect(screen.getByRole('status')).toHaveTextContent('/editor');
  fireEvent.click(screen.getByRole('button', { name: 'Keep editing' }));
  await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  expect(screen.getByRole('status')).toHaveTextContent('/editor');
  fireEvent.click(screen.getByRole('link', { name: 'Elsewhere' }));
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }));
  await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('/elsewhere?tab=b'));
});

it('ignores same-page links and modified clicks', () => {
  mount();
  fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
  fireEvent.click(screen.getByRole('link', { name: 'Same page' }));
  fireEvent.click(screen.getByRole('link', { name: 'Sessions' }), { metaKey: true });
  expect(screen.queryByRole('dialog')).toBeNull();
});

it('warns on unload only while dirty and confirms its own discard', async () => {
  mount();
  const clean = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(clean);
  expect(clean.defaultPrevented).toBe(false);
  fireEvent.click(screen.getByRole('button', { name: 'Cancel editing' }));
  await waitFor(() => expect(discarded).toBe(true));
  fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
  const dirty = new Event('beforeunload', { cancelable: true });
  window.dispatchEvent(dirty);
  expect(dirty.defaultPrevented).toBe(true);
  fireEvent.click(screen.getByRole('button', { name: 'Cancel editing' }));
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }));
  await waitFor(() => expect(discarded).toBe(true));
});

it('resumes the original link after confirmation, keeping replace and its onClick', async () => {
  mount();
  fireEvent.click(screen.getByRole('button', { name: 'Edit' }));
  fireEvent.click(screen.getByRole('link', { name: 'Inbox' }));
  expect(onNavigate).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole('button', { name: 'Discard changes' }));
  await waitFor(() => expect(screen.getByRole('status')).toHaveTextContent('/inbox'));
  expect(screen.getByTestId('navigation-type')).toHaveTextContent('REPLACE');
  expect(onNavigate).toHaveBeenCalledOnce();
  expect(screen.queryByRole('dialog')).toBeNull();
});
