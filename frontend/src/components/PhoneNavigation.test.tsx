// @vitest-environment jsdom
import { act, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { createMemoryRouter, MemoryRouter, Route, RouterProvider, Routes, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PhoneNavigation } from './PhoneNavigation';

const plugins = vi.hoisted(() => ({ pages: [] as { ownerId: string; pluginId: string; page: { id: string; label: string } }[] }));
vi.mock('../lib/pluginUI', async (original) => ({ ...await original<typeof import('../lib/pluginUI')>(), usePluginUIs: () => ({ uis: [], loading: false, error: false }) }));
vi.mock('../lib/pluginPages', async (original) => ({ ...await original<typeof import('../lib/pluginPages')>(), usePluginPages: () => ({ pages: plugins.pages }) }));

let phone: boolean;
let listeners: Set<() => void>;
const change = () => listeners.forEach(listener => listener());
const removeEventListener = vi.fn();

beforeEach(() => {
  plugins.pages = [];
  phone = true;
  listeners = new Set();
  vi.stubGlobal('matchMedia', vi.fn(() => ({
    matches: phone,
    addEventListener: (_: string, listener: () => void) => { listeners.add(listener); },
    removeEventListener: (_: string, listener: () => void) => { listeners.delete(listener); removeEventListener(); },
  })));
});
afterEach(() => vi.unstubAllGlobals());

function mount(path = '/sessions', unreadTotal = 0, state?: object, unreadSessions = 0) {
  return render(<MemoryRouter initialEntries={[state ? { pathname: path, state } : path]}>
    <PhoneNavigation unreadTotal={unreadTotal} unreadSessions={unreadSessions} />
    <Routes><Route path="*" element={<div>Current page</div>} /></Routes>
  </MemoryRouter>);
}

describe('phone navigation', () => {
  it('replaces More with a plugin destination and returns directly to the previous page', async () => {
    plugins.pages = [{ ownerId: 'machine', pluginId: 'org.example.pentest', page: { id: 'pentest', label: 'Pentest' } }];
    function History() {
      const location = useLocation();
      const navigate = useNavigate();
      return <><output>{location.pathname + location.search}</output><button onClick={() => navigate(-1)}>Back</button><button onClick={() => navigate(1)}>Forward</button></>;
    }
    const router = createMemoryRouter([{ path: '*', element: <><PhoneNavigation unreadTotal={0} /><History /></> }], { initialEntries: ['/projects', '/sessions'] });
    const navigations: string[] = [];
    router.subscribe(state => navigations.push(state.historyAction));
    render(<RouterProvider router={router} />);
    const user = userEvent.setup();
    await user.click(screen.getByRole('button', { name: 'More' }));
    navigations.length = 0;
    await user.click(screen.getByRole('link', { name: 'Pentest' }));
    expect(navigations).toEqual(['REPLACE']);
    expect(screen.getByRole('status')).toHaveTextContent('/plugins/org.example.pentest/pentest?ownerId=machine');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Back' }));
    expect(screen.getByRole('status')).toHaveTextContent('/sessions');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Forward' }));
    expect(screen.getByRole('status')).toHaveTextContent('/plugins/org.example.pentest/pentest?ownerId=machine');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

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

  it.each([0, 1, 12, 120])('shows %i unread sessions on Home in More destinations', async (count) => {
    mount('/sessions', 7, undefined, count);
    await userEvent.setup().click(screen.getByRole('button', { name: 'More' }));
    const home = screen.getByRole('link', { name: count ? `Home, ${count} unread sessions` : 'Home' });
    expect(home).toHaveTextContent(count ? (count > 99 ? '99+' : String(count)) : /^Home$/);
    expect(home).toHaveAttribute('href', '/');
    expect(screen.getByRole('link', { name: 'Inbox, 7 unread messages' })).toHaveTextContent('7');
  });

  it('keeps Projects selected inside an owner-qualified project', () => {
    mount('/project/%2Frepo/worktrees?remoteId=remote-1');
    expect(screen.getByRole('link', { name: 'Projects' })).toHaveAttribute('aria-current', 'page');
  });

  it.each(['/session/ses-1', '/session/new'])('leaves %s full-height without a tab bar', path => {
    mount(path);
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
  });

  it.each(['/session/ses-1', '/session/new'])('exposes navigation in the %s session list', path => {
    mount(path, 0, { phoneOverlay: { name: 'conversation-panel', value: 'sidebar', scope: path } });
    expect(screen.getByRole('link', { name: 'Sessions' })).toHaveAttribute('aria-current', 'page');
    expect(screen.getByRole('button', { name: 'More' })).not.toHaveClass('active');
  });

  it('keeps navigation mounted while More is open from a conversation', () => {
    const path = '/session/ses-1';
    mount(path, 0, { phoneOverlay: { name: 'navigation', value: 'more', scope: path } });
    expect(screen.getByRole('dialog', { name: 'More destinations' })).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'More' })).toHaveClass('active');
  });

  it.each([
    { name: 'conversation-panel', value: 'details', scope: '/session/ses-1' },
    { name: 'conversation-panel', value: 'sidebar', scope: '/session/other' },
  ])('keeps conversation navigation hidden for %j', phoneOverlay => {
    mount('/session/ses-1', 0, { phoneOverlay });
    expect(screen.queryByRole('navigation')).not.toBeInTheDocument();
  });

  it('opens the shared modal, closes with Escape, and restores focus', async () => {
    const user = userEvent.setup();
    mount();
    const more = screen.getByRole('button', { name: 'More' });
    await user.click(more);
    const dialog = screen.getByRole('dialog', { name: 'More destinations' });
    for (const label of ['Factory', 'Routines', 'Artifacts', 'Analytics', 'Provider usage', 'Settings', 'Home']) {
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
