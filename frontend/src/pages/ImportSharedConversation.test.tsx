// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { ImportSharedConversation } from './ImportSharedConversation';
import { api } from '../lib/api';
import { getDraft } from '../lib/composerDraft';
import type { SharedConversation } from '../lib/api.types';

const navigate = vi.fn();
vi.mock('react-router-dom', async () => {
  const actual = await vi.importActual<typeof import('react-router-dom')>('react-router-dom');
  return {
    ...actual,
    useNavigate: () => navigate,
    useSearchParams: () => [new URLSearchParams({ url: 'https://relay.test/v/share-1#k=key' })],
  };
});

const readRelayShare = vi.fn();
vi.mock('../lib/relayShare', async () => {
  const actual = await vi.importActual<typeof import('../lib/relayShare')>('../lib/relayShare');
  return { ...actual, readRelayShare: (...args: unknown[]) => readRelayShare(...args) };
});

const conversation: SharedConversation = {
  session: { id: 'remote-1', title: 'Debug the parser', directory: '/elsewhere' } as never,
  messages: [
    { id: 'm1', sessionId: 'remote-1', timeCreated: 1, data: { role: 'user' } },
    { id: 'm2', sessionId: 'remote-1', timeCreated: 2, data: { role: 'assistant' } },
  ],
  parts: [
    { id: 'p1', messageId: 'm1', sessionId: 'remote-1', timeCreated: 1, data: { type: 'text', text: 'rm -rf /' } },
    { id: 'p2', messageId: 'm2', sessionId: 'remote-1', timeCreated: 2, data: { type: 'text', text: 'the fix is here' } },
  ],
  readOnly: true,
};

function renderPage() {
  return render(
    <MemoryRouter>
      <ImportSharedConversation />
    </MemoryRouter>,
  );
}

describe('ImportSharedConversation', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    localStorage.clear();
    readRelayShare.mockResolvedValue({ chunks: [conversation], last: 0 });
    vi.spyOn(api, 'projects').mockResolvedValue([
      { directory: '/local/repo' },
      { directory: '/remote/repo', remoteId: 'r1' },
      { directory: '/local/other' },
    ] as never);
    vi.spyOn(api, 'createSession').mockResolvedValue({ id: 'new-session' } as never);
  });

  it('offers only local projects as fork targets', async () => {
    renderPage();

    const select = await screen.findByRole('combobox');
    const options = [...select.querySelectorAll('option')].map((o) => o.value);
    // A cross-machine fork must land locally: a remote target would
    // push the imported prompt onto yet another machine.
    expect(options).toEqual(['/local/repo', '/local/other']);
    expect(select).toHaveValue('/local/repo');
  });

  it('parks the transcript as an unsent draft and opens the new session', async () => {
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: /create local fork/i }));

    await waitFor(() => expect(navigate).toHaveBeenCalledWith('/session/new-session'));
    expect(api.createSession).toHaveBeenCalledWith('/local/repo', undefined, 'Debug the parser');

    // Imported content is untrusted: it must be wrapped and left in the
    // composer rather than sent.
    const draft = getDraft('new-session');
    expect(draft).toContain('reference only');
    expect(draft).toContain('--- BEGIN IMPORTED CONVERSATION ---');
    expect(draft).toContain('--- END IMPORTED CONVERSATION ---');
    expect(draft).toContain('## User');
    expect(draft).toContain('## Assistant');
    expect(draft).toContain('rm -rf /');
    expect(draft).toContain('the fix is here');
  });

  it('forks into the project the user picks', async () => {
    renderPage();
    fireEvent.change(await screen.findByRole('combobox'), { target: { value: '/local/other' } });
    fireEvent.click(screen.getByRole('button', { name: /create local fork/i }));

    await waitFor(() =>
      expect(api.createSession).toHaveBeenCalledWith('/local/other', undefined, 'Debug the parser'),
    );
  });

  it('reports a share that cannot be read instead of rendering a fork form', async () => {
    readRelayShare.mockRejectedValue(new Error('relay share: 404'));
    renderPage();

    expect(await screen.findByRole('alert')).toHaveTextContent('relay share: 404');
    expect(screen.queryByRole('button', { name: /create local fork/i })).toBeNull();
  });

  it('keeps the user on the page when session creation fails', async () => {
    vi.spyOn(api, 'createSession').mockRejectedValue(new Error('opencode is unreachable'));
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: /create local fork/i }));

    expect(await screen.findByRole('alert')).toHaveTextContent('opencode is unreachable');
    expect(navigate).not.toHaveBeenCalled();
    // The button must return to an actionable state so a retry is possible.
    await waitFor(() =>
      expect(screen.getByRole('button', { name: /create local fork/i })).not.toBeDisabled(),
    );
  });

  it('falls back to a generic title when the shared session has none', async () => {
    readRelayShare.mockResolvedValue({ chunks: [{ ...conversation, session: null }], last: 0 });
    renderPage();
    fireEvent.click(await screen.findByRole('button', { name: /create local fork/i }));

    await waitFor(() =>
      expect(api.createSession).toHaveBeenCalledWith('/local/repo', undefined, 'Forked conversation'),
    );
  });
});
