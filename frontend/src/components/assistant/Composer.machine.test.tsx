// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { api } from '../../lib/api';
import { useShortcutDispatcher } from '../../lib/shortcutRegistry';
import { Composer } from './Composer';

function WithShortcuts(props: React.ComponentProps<typeof Composer>) {
  useShortcutDispatcher();
  return <Composer {...props} />;
}

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
async function pickMachine(name: string) {
  fireEvent.click(await screen.findByRole('combobox', { name: 'Session machine' }));
  fireEvent.click(screen.getByRole('option', { name }));
}
const target = { remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', dir: '/remote/project' };

it('locks machine selection when the composer is disabled by a shared pending start', async () => {
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  render(<Composer isRunning={false} disabled newConversation directory="/repo" onMachineChange={vi.fn()} />);
  expect(await screen.findByRole('combobox', { name: 'Session machine' })).toBeDisabled();
});

it('switches a new conversation, keeping its typed draft, and locks sending during the switch', async () => {
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  let complete!: () => void;
  const onMachineChange = vi.fn(() => new Promise<void>((resolve) => { complete = resolve; }));
  render(<Composer isRunning={false} newConversation directory="/local/project" sessionId="machine-test" onMachineChange={onMachineChange} />);
  const input = screen.getByRole('textbox');
  fireEvent.input(input, { target: { value: 'a draft just typed' } });
  await pickMachine('Build box');
  expect(onMachineChange).toHaveBeenCalledWith(target);
  // The composer stays mounted across the switch, so the text survives.
  expect(input).toHaveValue('a draft just typed');
  expect(input).toBeDisabled();
  expect(screen.getByRole('combobox', { name: 'Session machine' })).toBeDisabled();
  await act(async () => complete());
  await waitFor(() => expect(input).not.toBeDisabled());
});

it('renders the machine selector with an icon after the worktree selector', async () => {
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  render(<Composer isRunning={false} newConversation directory="/local/project" sessionId="order-test" onMachineChange={vi.fn()} />);
  const machine = await screen.findByTestId('composer-machine-selector');
  const worktree = screen.getByTestId('composer-target-selector');
  expect(worktree.compareDocumentPosition(machine) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
  expect(machine.querySelector('.bi-pc-display')).not.toBeNull();
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
  await pickMachine('Build box');
  fireEvent.drop(screen.getByRole('textbox'), { dataTransfer: { files: [new File(['hello'], 'note.txt', { type: 'text/plain' })] } });
  expect(upload).not.toHaveBeenCalled();
  await act(async () => complete());
});

function fakeSpeechRecognition() {
  vi.stubGlobal('isSecureContext', true);
  const recognition = {
    start: vi.fn(), stop: vi.fn(), abort: vi.fn(),
    onend: null as null | (() => void),
    onresult: null as null | ((e: unknown) => void),
  };
  vi.stubGlobal('webkitSpeechRecognition', function Recognition() { return recognition; });
  return recognition;
}

it('locks machine selection until dictation delivers its final result', async () => {
  const recognition = fakeSpeechRecognition();
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  render(<Composer isRunning={false} newConversation directory="/local/project" sessionId="dictation-test" onMachineChange={vi.fn()} />);
  const machine = await screen.findByRole('combobox', { name: 'Session machine' });
  await act(async () => fireEvent.click(screen.getByTitle('Record voice message')));
  expect(machine).toBeDisabled();
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Stop recording' })));
  // The final result arrives after stop(); switching before it would drop it.
  expect(machine).toBeDisabled();
  const final = Object.assign([{ transcript: 'last words' }], { isFinal: true });
  act(() => recognition.onresult!({ resultIndex: 0, results: [final] }));
  act(() => recognition.onend!());
  expect(screen.getByRole('textbox')).toHaveValue('last words');
  expect(machine).not.toBeDisabled();
});

it('locks machine selection while microphone access is pending', async () => {
  let grant!: (stream: unknown) => void;
  Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia: () => new Promise((resolve) => { grant = resolve; }) } });
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  render(<Composer isRunning={false} newConversation whisperAvailable directory="/local/project" sessionId="acquire-test" onMachineChange={vi.fn()} />);
  const machine = await screen.findByRole('combobox', { name: 'Session machine' });
  await act(async () => fireEvent.click(screen.getByTitle('Record voice message')));
  expect(machine).toBeDisabled();
  const node = { connect: vi.fn(), disconnect: vi.fn(), onaudioprocess: null };
  vi.stubGlobal('AudioContext', function Ctx() {
    return { sampleRate: 16000, destination: {}, close: vi.fn(), createMediaStreamSource: () => node, createScriptProcessor: () => node };
  });
  await act(async () => grant({ getTracks: () => [{ stop: vi.fn() }] }));
  expect(screen.getByRole('button', { name: 'Stop recording' })).toBeInTheDocument();
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Stop recording' })));
  expect(machine).not.toBeDisabled();
  delete (navigator as { mediaDevices?: unknown }).mediaDevices;
});

it('locks machine selection until a transcription lands', async () => {
  // jsdom is not a secure context, so the Speech API is skipped for whisper.
  const track = { stop: vi.fn() };
  Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia: vi.fn().mockResolvedValue({ getTracks: () => [track] }) } });
  const node = { connect: vi.fn(), disconnect: vi.fn(), onaudioprocess: null };
  vi.stubGlobal('AudioContext', function Ctx() {
    return { sampleRate: 16000, destination: {}, close: vi.fn(), createMediaStreamSource: () => node, createScriptProcessor: () => node };
  });
  let finish!: (text: string) => void;
  vi.spyOn(api, 'transcribe').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  render(<Composer isRunning={false} newConversation whisperAvailable directory="/local/project" sessionId="whisper-test" onMachineChange={vi.fn()} />);
  const machine = await screen.findByRole('combobox', { name: 'Session machine' });
  await act(async () => fireEvent.click(screen.getByTitle('Record voice message')));
  // Enough samples that the WAV is non-empty and gets transcribed.
  (node.onaudioprocess as unknown as (e: unknown) => void)({ inputBuffer: { getChannelData: () => new Float32Array(1600) } });
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Stop recording' })));
  expect(machine).toBeDisabled();
  await act(async () => finish('spoken words'));
  expect(screen.getByRole('textbox')).toHaveValue('spoken words');
  expect(machine).not.toBeDisabled();
  delete (navigator as { mediaDevices?: unknown }).mediaDevices;
});

it('closes an open machine menu when dictation starts', async () => {
  fakeSpeechRecognition();
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  const onMachineChange = vi.fn();
  render(<WithShortcuts isRunning={false} newConversation directory="/local/project" sessionId="open-menu-dictation" onMachineChange={onMachineChange} />);
  const machine = await screen.findByRole('combobox', { name: 'Session machine' });
  const input = screen.getByRole('textbox');
  fireEvent.click(machine);
  const option = screen.getByRole('option', { name: 'Build box' });
  await act(async () => fireEvent.keyDown(input, { code: 'KeyD', altKey: true }));
  expect(machine).toBeDisabled();
  expect(screen.queryByRole('option', { name: 'Build box' })).not.toBeInTheDocument();
  option.click();
  expect(onMachineChange).not.toHaveBeenCalled();
});

it('does not start dictation while switching machines', async () => {
  const recognition = fakeSpeechRecognition();
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  let complete!: () => void;
  render(<WithShortcuts isRunning={false} newConversation directory="/local/project" sessionId="switch-mic-test" onMachineChange={() => new Promise<void>((resolve) => { complete = resolve; })} />);
  await pickMachine('Build box');
  await act(async () => fireEvent.click(screen.getByTitle('Record voice message')));
  fireEvent.keyDown(screen.getByRole('textbox'), { code: 'KeyD', altKey: true });
  expect(recognition.start).not.toHaveBeenCalled();
  await act(async () => complete());
});

it('does not start a second dictation while a transcription is pending', async () => {
  const getUserMedia = vi.fn().mockResolvedValue({ getTracks: () => [{ stop: vi.fn() }] });
  Object.defineProperty(navigator, 'mediaDevices', { configurable: true, value: { getUserMedia } });
  const node = { connect: vi.fn(), disconnect: vi.fn(), onaudioprocess: null };
  vi.stubGlobal('AudioContext', function Ctx() {
    return { sampleRate: 16000, destination: {}, close: vi.fn(), createMediaStreamSource: () => node, createScriptProcessor: () => node };
  });
  let finish!: (text: string) => void;
  vi.spyOn(api, 'transcribe').mockReturnValue(new Promise((resolve) => { finish = resolve; }));
  vi.spyOn(api, 'resolveTargets').mockResolvedValue({ candidates: [target], remotes: [target] });
  vi.spyOn(api, 'gitBranches').mockResolvedValue({ branches: [] });
  render(<WithShortcuts isRunning={false} newConversation whisperAvailable directory="/local/project" sessionId="overlap-test" onMachineChange={vi.fn()} />);
  const machine = await screen.findByRole('combobox', { name: 'Session machine' });
  const input = screen.getByRole('textbox');
  await act(async () => fireEvent.keyDown(input, { code: 'KeyD', altKey: true }));
  expect(getUserMedia).toHaveBeenCalledTimes(1);
  (node.onaudioprocess as unknown as (e: unknown) => void)({ inputBuffer: { getChannelData: () => new Float32Array(1600) } });
  await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Stop recording' })));
  await act(async () => fireEvent.keyDown(input, { code: 'KeyD', altKey: true }));
  await act(async () => fireEvent.click(screen.getByTitle('Record voice message')));
  expect(getUserMedia).toHaveBeenCalledTimes(1);
  await act(async () => finish('first take'));
  expect(machine).not.toBeDisabled();
  delete (navigator as { mediaDevices?: unknown }).mediaDevices;
});
