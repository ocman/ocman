// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { clearDraft, getDraft, saveDraft } from '../../lib/composerDraft';
import { api } from '../../lib/api';
import { Composer } from './Composer';

beforeEach(() => { clearDraft('new'); clearDraft('s1'); vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [], remotes: [] }); });
afterEach(() => { vi.restoreAllMocks(); });

// A new conversation has no session: its draft lives under its own key and
// the platform catalog is not fetched for a session that does not exist.
it('keys drafts by draftKey when there is no session', () => {
  const commands = vi.spyOn(api, 'commands');
  saveDraft('new', 'typed earlier');
  const view = render(<Composer isRunning={false} directory="/repo" newConversation draftKey="new" />);
  const input = screen.getByRole('textbox');
  expect(input).toHaveValue('typed earlier');
  fireEvent.input(input, { target: { value: 'typed now' } });
  view.unmount();
  expect(getDraft('new')).toBe('typed now');
  expect(getDraft('s1')).toBe('');
  expect(commands).not.toHaveBeenCalled();
});

it('falls back to the session id as draft key', () => {
  vi.spyOn(api, 'commands').mockResolvedValue([]);
  saveDraft('s1', 'session draft');
  render(<Composer isRunning={false} sessionId="s1" />);
  expect(screen.getByRole('textbox')).toHaveValue('session draft');
});
