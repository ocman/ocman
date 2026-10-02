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

it('locks machine selection for an upload still in flight', async () => {
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  let finish!: (value: Awaited<ReturnType<typeof api.uploadComposerAttachment>>) => void;
  vi.spyOn(api, 'uploadComposerAttachment').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  render(<Composer isRunning={false} newConversation directory="/local/project" sessionId="upload-test" onMachineChange={vi.fn()} />);
  const machine = await screen.findByRole('combobox', { name: 'Session machine' });
  fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['hello'], 'note.txt', { type: 'text/plain' })] } });
  expect(machine).toBeDisabled();
  await act(async () => finish({ path: '/source/note.txt', name: 'note.txt', mime: 'text/plain', size: 5 }));
  expect(machine).toBeDisabled();
  fireEvent.click(screen.getByRole('button', { name: /Remove attached file/ }));
  expect(machine).not.toBeDisabled();
});

it('rejects file drops while switching machines', async () => {
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  const upload = vi.spyOn(api, 'uploadComposerAttachment');
  let complete!: () => void;
  render(<Composer isRunning={false} newConversation directory="/local/project" sessionId="switch-test" onMachineChange={() => new Promise<void>((resolve) => { complete = resolve; })} />);
  fireEvent.change(await screen.findByRole('combobox', { name: 'Session machine' }), { target: { value: 'box' } });
  fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['hello'], 'note.txt', { type: 'text/plain' })] } });
  expect(upload).not.toHaveBeenCalled();
  await act(async () => complete());
});
