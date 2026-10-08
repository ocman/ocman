// @vitest-environment jsdom
import { expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { FirstSubmissionNotice } from './FirstSubmissionNotice';
import { discardFirstSubmission, reconcileFirstSubmission } from './firstSubmission';

vi.mock('./firstSubmission', () => ({
  useFirstSubmission: (select: (state: unknown) => unknown) => select({ entries: { orphan: { text: 'payload', pending: true, error: 'Unknown outcome', canRelease: true } } }),
  discardFirstSubmission: vi.fn(), reconcileFirstSubmission: vi.fn(), startFirstSubmission: vi.fn(),
}));

it('retains a visible error if explicit release fails, and lets the user reconcile again', async () => {
  vi.spyOn(window, 'confirm').mockReturnValue(true);
  vi.mocked(discardFirstSubmission).mockRejectedValue(new Error('release write failed'));
  vi.mocked(reconcileFirstSubmission).mockResolvedValue(undefined);
  render(<FirstSubmissionNotice sessionId="orphan" />);
  fireEvent.click(screen.getByRole('button', { name: 'Release first-delivery lock' }));
  expect(await screen.findByText(/release write failed/)).toBeInTheDocument();
  fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
  expect(reconcileFirstSubmission).toHaveBeenCalledWith('orphan');
});
