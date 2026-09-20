// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, useLocation } from 'react-router-dom';
import { describe, expect, it, vi } from 'vitest';
import { PromptToastNotify } from './PromptToastNotify';

vi.mock('../lib/useGlobalEvents', () => ({ useGlobalEvents: vi.fn() }));
vi.mock('../lib/useToastNotify', () => ({ useToastNotify: () => ({
  toasts: [{ toastId: 'permission', kind: 'permission', sessionId: 'child', title: 'Review' }, { toastId: 'question', kind: 'question', sessionId: 'question-session', title: 'Question' }],
  dismiss: vi.fn(),
}) }));

function Location() { const location = useLocation(); return <output aria-label="Current route">{location.pathname}{location.search}</output>; }

describe('PromptToastNotify', () => {
  it('opens the inbox for permissions and the session for questions', async () => {
    render(<MemoryRouter><PromptToastNotify /><Location /></MemoryRouter>);
    fireEvent.click(await screen.findByRole('button', { name: 'Open inbox' }));
    expect(screen.getByLabelText('Current route')).toHaveTextContent('/inbox?category=permission');
    fireEvent.click(screen.getByRole('button', { name: 'Open session' }));
    expect(screen.getByLabelText('Current route')).toHaveTextContent('/session/question-session');
  });
});
