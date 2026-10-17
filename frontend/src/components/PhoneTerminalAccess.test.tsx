// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter, useNavigate, type NavigateFunction } from 'react-router-dom';
import { PhoneTerminalAccess } from './PhoneTerminalAccess';

let slot: HTMLDivElement;
beforeEach(() => {
  vi.stubGlobal('matchMedia', () => ({ matches: true, addEventListener() {}, removeEventListener() {} }));
  slot = document.createElement('div');
  slot.id = 'phone-terminal-slot';
  document.body.appendChild(slot);
});
afterEach(() => { slot.remove(); vi.unstubAllGlobals(); });

function setup() {
  let navigate!: NavigateFunction;
  function Harness() {
    navigate = useNavigate();
    return <PhoneTerminalAccess><div>Terminal contents</div></PhoneTerminalAccess>;
  }
  const view = render(<MemoryRouter initialEntries={['/session/one']}><Harness /></MemoryRouter>);
  return { ...view, back: () => act(async () => { await navigate(-1); }), user: userEvent.setup() };
}

describe('phone terminal access', () => {
  it('launches from details, closes with its button, and leaves no footer', async () => {
    const { user } = setup();
    const launcher = screen.getByRole('button', { name: 'Open terminal' });
    expect(slot).toContainElement(launcher);
    expect(screen.queryByText('Terminal contents')).not.toBeInTheDocument();
    await user.click(launcher);
    expect(screen.getByRole('dialog', { name: 'Terminal' })).toHaveTextContent('Terminal contents');
    await user.click(screen.getByRole('button', { name: 'Close terminal view' }));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('native back and Escape close the terminal', async () => {
    const { user, back } = setup();
    await user.click(screen.getByRole('button', { name: 'Open terminal' }));
    await back();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Open terminal' }));
    await user.keyboard('{Escape}');
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('reattaches when details replaces its strip and tolerates a missing slot', async () => {
    const { unmount } = setup();
    slot.remove();
    await waitFor(() => expect(screen.queryByRole('button', { name: 'Open terminal' })).not.toBeInTheDocument());
    slot = document.createElement('div');
    slot.id = 'phone-terminal-slot';
    document.body.appendChild(slot);
    await waitFor(() => expect(slot).toContainElement(screen.getByRole('button', { name: 'Open terminal' })));
    unmount();
    expect(slot).toBeEmptyDOMElement();
  });
});
