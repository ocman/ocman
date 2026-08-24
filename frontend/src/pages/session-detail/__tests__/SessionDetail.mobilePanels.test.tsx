// @vitest-environment jsdom
//
// Phone overlay panels (#mobile layout): the sessions drawer and the
// details overlay are toggled from header buttons portalled into
// #header-actions-slot. The buttons only *display* on <=768px via
// CSS, but their behaviour (state, Escape, auto-close on selection,
// seeding a pane into a collapsed right panel) is viewport-independent
// and testable in jsdom.

import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, screen, waitFor } from '@testing-library/react';
import { flushPromises, makeSession, makeSessionDetail, renderSessionPage } from './harness';
import { useUiStore } from '../../../lib/uiStore';

let slot: HTMLDivElement;

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn() as unknown as typeof Element.prototype.scrollIntoView;
  (Element.prototype as unknown as { scrollTo: () => void }).scrollTo = vi.fn();
  // The toggles portal into the header slot normally rendered by
  // <Header> in App.tsx; the harness renders SessionDetail alone.
  slot = document.createElement('div');
  slot.id = 'header-actions-slot';
  document.body.appendChild(slot);
});

afterEach(() => {
  slot.remove();
  vi.restoreAllMocks();
});

describe('SessionDetail — phone overlay panels', () => {
  it('toggles the sessions drawer and closes it on Escape', async () => {
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    const layout = screen.getByTestId('session-layout');
    const toggle = screen.getByTestId('mobile-sessions-toggle');
    expect(layout.className).not.toContain('mobile-sidebar-open');
    expect(toggle).toHaveAttribute('aria-expanded', 'false');

    fireEvent.click(toggle);
    expect(screen.getByTestId('session-layout').className).toContain('mobile-sidebar-open');
    expect(screen.getByTestId('mobile-sessions-toggle')).toHaveAttribute('aria-expanded', 'true');

    fireEvent.keyDown(window, { key: 'Escape' });
    expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
  });

  it('clicking the same toggle again closes the drawer', async () => {
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-sidebar-open');
    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
  });

  it('the two panels are mutually exclusive', async () => {
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    const layout = screen.getByTestId('session-layout');
    expect(layout.className).not.toContain('mobile-sidebar-open');
    expect(layout.className).toContain('mobile-details-open');
  });

  it('opening details with a collapsed right panel seeds the info pane', async () => {
    useUiStore.setState({ changesSidebarOpenTabs: [] });
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-details-open');
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['info']);
  });

  it('opening details with panes already open keeps them', async () => {
    useUiStore.setState({ changesSidebarOpenTabs: ['session'] });
    renderSessionPage({ sessionId: 'sess_1' });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    expect(useUiStore.getState().changesSidebarOpenTabs).toEqual(['session']);
  });

  it('closes an open overlay on an external route change (palette, redirect)', async () => {
    const other = makeSession({ id: 'sess_2', title: 'Other session' });
    const active = makeSession({ id: 'sess_1' });
    const handle = renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(active),
      sessions: [active, other],
    });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-details-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-details-open');

    // Navigate WITHOUT going through the drawer — e.g. the command
    // palette or the closed-session-reopen shortcut.
    handle.navigate('/session/sess_2');

    await waitFor(() => {
      expect(screen.getByTestId('session-layout').className).not.toContain('mobile-details-open');
    });
  });

  it('selecting a session from the drawer closes it', async () => {
    const other = makeSession({ id: 'sess_2', title: 'Other session' });
    const active = makeSession({ id: 'sess_1' });
    renderSessionPage({
      sessionId: 'sess_1',
      detail: makeSessionDetail(active),
      sessions: [active, other],
    });
    await flushPromises();

    fireEvent.click(screen.getByTestId('mobile-sessions-toggle'));
    expect(screen.getByTestId('session-layout').className).toContain('mobile-sidebar-open');

    const row = await screen.findByText('Other session');
    fireEvent.click(row);

    await waitFor(() => {
      expect(screen.getByTestId('session-layout').className).not.toContain('mobile-sidebar-open');
    });
  });
});
