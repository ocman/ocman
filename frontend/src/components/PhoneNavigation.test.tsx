// @vitest-environment jsdom
import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PhoneNavigation } from './PhoneNavigation';

let phone: boolean;
let change: () => void;
const removeEventListener = vi.fn();

beforeEach(() => {
  phone = true;
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: phone,
    addEventListener: (_: string, listener: () => void) => { change = listener; },
    removeEventListener,
  })));
});
afterEach(() => vi.unstubAllGlobals());

function mount(path = '/sessions', unreadTotal = 0) {
  return render(<MemoryRouter initialEntries={[path]}>
    <PhoneNavigation unreadTotal={unreadTotal} />
    <Routes><Route path="*" element={<div>Current page</div>} /></Routes>
  </MemoryRouter>);
}

describe('phone navigation', () => {
  it('renders four destinations and marks the selected tab', () => {
    mount();
    expect(window.matchMedia).toHaveBeenCalledWith('(max-width: 640px)');
    const nav = screen.getByRole('navigation', { name: 'Phone navigation' });
    expect(within(nav).getAllByRole('link')).toHaveLength(3);
    expect(within(nav).getByRole('button', { name: 'More' })).toHaveAttribute('aria-expanded', 'false');
    expect(within(nav).getByRole('link', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page');
    expect(within(nav).getByRole('link', { name: 'Projects' })).not.toHaveAttribute('aria-current');
  });

  it.each([1, 12, 120])('announces %i unread messages', (count) => {
    mount('/inbox', count);
    expect(screen.getByRole('link', { name: `Inbox, ${count} unread messages` })).toHaveTextContent(count > 99 ? '99+' : String(count));
  });

  it('keeps Projects selected inside an owner-qualified project', () => {
    mount('/project/%2Frepo/worktrees?remoteId=remote-1');
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('aria-current', 'page');
  });

  it.each(['/session/ses-1', '/session/new'])('leaves %s full-height without a tab bar', path => {
    mount(path);
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
  });

  it('opens the shared modal, closes with Escape, and restores focus', async () => {
    const user = userEvent.setup();
    mount();
    const more = screen.getByRole('button', { name: 'More' });
    await user.click(more);
    const dialog = screen.getByRole('dialog', { name: 'More destinations' });
    for (const label of ['Factory', 'Routines', 'Artifacts', 'Analytics', 'Usage', 'Settings', 'Home']) {
      expect(within(dialog).getByRole('link', { name: label })).toBeInTheDocument();
    }
    await user.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(more).toHaveFocus();
  });

  it('closes after secondary navigation and highlights More', async () => {
    const user = userEvent.setup();
    mount();
    await user.click(screen.getByRole('button', { name: 'More' }));
    await user.click(screen.getByRole('link', { name: 'Settings' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'More' })).toHaveClass('active');
    await user.click(screen.getByRole('button', { name: 'More' }));
    await user.click(screen.getByRole('button', { name: 'Close more destinations' }));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('unmounts the sheet and tab bar when resized to desktop', async () => {
    const user = userEvent.setup();
    mount();
    await user.click(screen.getByRole('button', { name: 'More' }));
    phone = false;
    act(() => change());
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    expect(removeEventListener).toHaveBeenCalled();
  });

  it('mounts only when resizing into phone widths', () => {
    phone = false;
    mount();
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
    phone = true;
    act(() => change());
    expect(screen.getByRole('navigation', { name: 'Phone navigation' })).toBeInTheDocument();
  });
});
