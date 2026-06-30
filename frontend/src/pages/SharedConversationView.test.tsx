// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SharedConversationView } from './SharedConversationView';
import { api } from '../lib/api';

// AssistantThread + runtime provider are heavy and irrelevant to the
// toggle wiring; stub them to a marker.
vi.mock('../components/OcmanRuntimeProvider', () => ({
  OcmanRuntimeProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('../components/AssistantThread', () => ({
  AssistantThread: () => <div data-testid="thread" />,
}));

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useParams: () => ({ token: 'tok' }) };
});

function renderView() {
  return render(
    <MemoryRouter>
      <SharedConversationView />
    </MemoryRouter>,
  );
}

describe('SharedConversationView collapse-tools toggle', () => {
  beforeEach(() => {
    vi.spyOn(api, 'sharedConversation').mockResolvedValue({
      session: { id: 's1', title: 'Hi', directory: '/repo' } as never,
      messages: [],
      parts: [],
      readOnly: true,
    });
  });

  it('collapses tool outputs by default and toggles via the checkbox', async () => {
    renderView();
    const view = await screen.findByTestId('shared-conversation');
    // Collapsed (compact PDF) is the default.
    expect(view).toHaveClass('oc-collapse-tools');

    const checkbox = screen
      .getByTestId('shared-collapse-tools')
      .querySelector('input') as HTMLInputElement;
    fireEvent.click(checkbox); // expand all
    await waitFor(() => expect(view).not.toHaveClass('oc-collapse-tools'));

    fireEvent.click(checkbox); // collapse again
    await waitFor(() => expect(view).toHaveClass('oc-collapse-tools'));
  });
});
