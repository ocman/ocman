// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SharedConversationView } from './SharedConversationView';
import type { SharedConversation } from '../lib/api.types';

vi.mock('../components/OcmanRuntimeProvider', () => ({
  OcmanRuntimeProvider: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock('../components/AssistantThread', () => ({
  AssistantThread: () => <div data-testid="thread" />,
}));

vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return { ...actual, useParams: () => ({ token: 'share-1' }) };
});

const readRelayShare = vi.fn();
vi.mock('../lib/relayShare', async () => {
  const actual = await vi.importActual<typeof import('../lib/relayShare')>('../lib/relayShare');
  return { ...actual, readRelayShare: (...args: unknown[]) => readRelayShare(...args), relayPollMs: 3000 };
});

function chunk(overrides: Partial<SharedConversation>): SharedConversation {
  return { session: null, messages: [], parts: [], readOnly: true, ...overrides };
}

function renderRelayView() {
  return render(
    <MemoryRouter>
      <SharedConversationView relay />
    </MemoryRouter>,
  );
}

describe('SharedConversationView (relay)', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    window.location.hash = '#k=the-key';
  });

  afterEach(() => {
    vi.useRealTimers();
    window.location.hash = '';
  });

  it('decrypts with the key from the fragment, which is never sent to the relay', async () => {
    readRelayShare.mockResolvedValue({
      chunks: [chunk({ session: { id: 's1', title: 'From the relay' } as never })],
      last: 0,
    });

    renderRelayView();

    await screen.findByTestId('shared-conversation');
    expect(readRelayShare).toHaveBeenCalledWith('share-1', 'the-key', 0, expect.anything());
    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('From the relay');
  });

  it('refuses to load without a key rather than asking the relay for one', async () => {
    window.location.hash = '';
    renderRelayView();

    expect(await screen.findByTestId('shared-error')).toHaveTextContent('Missing share decryption key');
    expect(readRelayShare).not.toHaveBeenCalled();
  });

  it('polls from the next sequence and merges later turns in', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    readRelayShare
      .mockResolvedValueOnce({
        chunks: [chunk({ session: { id: 's1', title: 'First' } as never })],
        last: 0,
      })
      .mockResolvedValueOnce({
        chunks: [chunk({ session: { id: 's1', title: 'Renamed later' } as never })],
        last: 1,
      })
      .mockResolvedValue({ chunks: [], last: -1 });

    renderRelayView();
    await screen.findByTestId('shared-conversation');

    await vi.advanceTimersByTimeAsync(3000);

    await waitFor(() =>
      expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Renamed later'),
    );
    // The second poll must resume after the last seen chunk, not refetch
    // the whole log every three seconds.
    expect(readRelayShare).toHaveBeenNthCalledWith(2, 'share-1', 'the-key', 1, expect.anything());
  });

  it('keeps the rendered conversation when a later poll returns nothing', async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    readRelayShare
      .mockResolvedValueOnce({ chunks: [chunk({ session: { id: 's1', title: 'Stable' } as never })], last: 0 })
      .mockResolvedValue({ chunks: [], last: -1 });

    renderRelayView();
    await screen.findByTestId('shared-conversation');

    await vi.advanceTimersByTimeAsync(9000);

    expect(screen.getByRole('heading', { level: 1 })).toHaveTextContent('Stable');
  });

  it('reports a failed load', async () => {
    readRelayShare.mockRejectedValue(new Error('relay share: 404'));
    renderRelayView();

    expect(await screen.findByTestId('shared-error')).toHaveTextContent('Failed to load or decrypt');
  });

  it('ignores an abort from unmounting mid-request', async () => {
    readRelayShare.mockRejectedValue(new DOMException('aborted', 'AbortError'));
    renderRelayView();

    // An abort is the normal teardown path, not an error to show.
    await waitFor(() => expect(readRelayShare).toHaveBeenCalled());
    expect(screen.queryByTestId('shared-error')).toBeNull();
    expect(screen.getByTestId('shared-loading')).toBeInTheDocument();
  });

  it('offers a local fork instead of a server-side markdown export', async () => {
    readRelayShare.mockResolvedValue({
      chunks: [chunk({ session: { id: 's1', title: 'Shared' } as never })],
      last: 0,
    });

    renderRelayView();
    await screen.findByTestId('shared-conversation');

    // Forking happens in the recipient's own ocman: the relay cannot
    // read the conversation, so it cannot render an export.
    expect(screen.getByTestId('shared-fork-local')).toHaveAttribute(
      'href',
      expect.stringContaining('/import-share?url='),
    );
    expect(screen.queryByTestId('shared-download-md')).toBeNull();
  });
});
