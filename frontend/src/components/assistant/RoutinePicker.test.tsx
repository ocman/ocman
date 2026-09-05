// @vitest-environment jsdom
import { beforeEach, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { api } from '../../lib/api';
import { RoutinePicker } from './RoutinePicker';

vi.mock('../../lib/api', () => ({ api: { routines: { list: vi.fn() } } }));

beforeEach(() => {
  vi.clearAllMocks();
  Element.prototype.scrollIntoView = vi.fn();
});

it('fetches routines and returns the selected routine', async () => {
  const selected = vi.fn();
  const routine = { id: 'r1', name: 'Review', prompt: 'Review this diff' };
  vi.mocked(api.routines.list).mockResolvedValue([routine] as never);
  render(<RoutinePicker open initialQuery="rev" onSelect={selected} onClose={() => {}} />);
  await userEvent.click(await screen.findByText('Review'));
  expect(selected).toHaveBeenCalledWith(routine);
});

it('shows fetch errors', async () => {
  vi.mocked(api.routines.list).mockRejectedValue(new Error('routines unavailable'));
  render(<RoutinePicker open onSelect={() => {}} onClose={() => {}} />);
  await waitFor(() => expect(screen.getByText('routines unavailable')).toBeInTheDocument());
});
