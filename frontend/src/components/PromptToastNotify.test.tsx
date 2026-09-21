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
  it('opens the target session for permissions and questions', async () => {
    render(<MemoryRouter><PromptToastNotify /><Location /></MemoryRouter>);
    const actions = await screen.findAllByRole('button', { name: 'Open session' });
    expect(actions).toHaveLength(2);
    fireEvent.click(actions[0]);
    expect(screen.getByLabelText('Current route')).toHaveTextContent('/session/child');
    fireEvent.click(actions[1]);
    expect(screen.getByLabelText('Current route')).toHaveTextContent('/session/question-session');
  });
});
