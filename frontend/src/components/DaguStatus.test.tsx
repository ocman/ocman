// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';

const { status } = vi.hoisted(() => ({ status: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { dagu: { status } } }));

import { DaguStatus } from './DaguStatus';

beforeEach(() => vi.clearAllMocks());

it('shows install guidance and rechecks availability', async () => {
  status
    .mockResolvedValueOnce({ status: 'unavailable', installCommand: 'brew install dagu' })
    .mockResolvedValueOnce({ status: 'compatible', version: '2.1.0', installCommand: 'brew install dagu' });
  const user = userEvent.setup();

  render(<DaguStatus remoteId="local" />);

  expect(await screen.findByText('Dagu is not installed')).toBeInTheDocument();
  expect(screen.getByText('brew install dagu')).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'Recheck Dagu' }));
  expect(await screen.findByText('Dagu 2.1.0 is compatible')).toBeInTheDocument();
  expect(status).toHaveBeenNthCalledWith(1, 'local', expect.any(AbortSignal));
  expect(status).toHaveBeenNthCalledWith(2, 'local', undefined);
});

it('reports unsupported versions without disabling adjacent content', async () => {
  status.mockResolvedValue({ status: 'unsupported', version: '1.15.0', installCommand: 'brew install dagu' });

  render(<><DaguStatus remoteId="remote-1" /><button>Schedule prompt</button></>);

  expect(await screen.findByText('Dagu 1.15.0 is unsupported')).toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Schedule prompt' })).toBeEnabled();
  expect(status).toHaveBeenCalledWith('remote-1', expect.any(AbortSignal));
});
