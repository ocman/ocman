// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { api } from '../../lib/api';
import { useMachinePlatforms } from '../../lib/useCapabilities';
import { DefaultPlatformSetting } from '../DefaultPlatformSetting';
import { ComposerPlatformSelector } from './ComposerPlatformSelector';

vi.mock('../../lib/useCapabilities', () => ({ useMachinePlatforms: vi.fn() }));
vi.mock('../../lib/api', () => ({ api: { getDefaultPlatform: vi.fn(), setDefaultPlatform: vi.fn() } }));

const entry = (id: string, displayName: string, remoteId?: string, composer = true) =>
  ({ id, displayName, available: true, remoteId, capabilities: { composer } });

beforeEach(() => {
  const all = [entry('opencode', 'OpenCode'), entry('claudecode', 'Claude Code'), entry('r-box:opencode', 'OpenCode', 'box')];
  vi.mocked(useMachinePlatforms).mockImplementation((remoteId = 'local') =>
    all.filter((p) => (p.remoteId ?? 'local') === remoteId) as unknown as ReturnType<typeof useMachinePlatforms>);
  vi.mocked(api.getDefaultPlatform).mockResolvedValue({ platform: 'opencode' });
  vi.mocked(api.setDefaultPlatform).mockImplementation(async (platform) => ({ platform }));
});

it('offers the machine\'s platforms and reports a switch', () => {
  const onSelect = vi.fn();
  render(<ComposerPlatformSelector platform="opencode" onSelect={onSelect} />);
  const select = screen.getByRole('combobox', { name: 'Session platform' });
  expect(select).toHaveTextContent('OpenCode');
  fireEvent.click(select);
  expect(screen.getAllByRole('option')).toHaveLength(2);
  fireEvent.click(screen.getByRole('option', { name: 'Claude Code' }));
  expect(onSelect).toHaveBeenCalledWith('claudecode');
});

it('is hidden when the machine has one platform', () => {
  const { container } = render(<ComposerPlatformSelector remoteId="box" platform="r-box:opencode" onSelect={vi.fn()} />);
  expect(container).toBeEmptyDOMElement();
});

it('saves the default platform setting', async () => {
  render(<DefaultPlatformSetting />);
  const select = await screen.findByRole('combobox', { name: 'Default platform for new conversations' });
  await waitFor(() => expect(select).toHaveTextContent('OpenCode'));
  fireEvent.click(select);
  fireEvent.click(screen.getByRole('option', { name: 'Claude Code' }));
  await waitFor(() => expect(api.setDefaultPlatform).toHaveBeenCalledWith('claudecode'));
});
