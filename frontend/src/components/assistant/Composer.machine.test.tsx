// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { api } from '../../lib/api';
import { Composer } from './Composer';

afterEach(() => vi.restoreAllMocks());
const target = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/remote/project' };

it('switches a new conversation with its current draft and locks sending during launch', async () => {
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  let complete!: () => void;
  const onMachineChange = vi.fn(() => new Promise<void>((resolve) => { complete = resolve; }));
  render(<Composer isRunning={false} newConversation directory="/local/project" sessionId="machine-test" onMachineChange={onMachineChange} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'a draft just typed' } });
  fireEvent.change(await screen.findByRole('combobox', { name: 'Session machine' }), { target: { value: 'box' } });
  expect(onMachineChange).toHaveBeenCalledWith(target, 'a draft just typed');
  expect(input).toBeDisabled();
  expect(screen.getByRole('combobox', { name: 'Session machine' })).toBeDisabled();
  await act(async () => complete());
  await waitFor(() => expect(input).not.toBeDisabled());
});

it('does not offer to move a conversation that already has messages', () => {
  const resolve = vi.spyOn(api, 'resolveTargets');
  render(<Composer isRunning={false} directory="/local/project" onMachineChange={vi.fn()} />);
  expect(screen.queryByRole('combobox', { name: 'Session machine' })).not.toBeInTheDocument();
  expect(resolve).not.toHaveBeenCalled();
});
