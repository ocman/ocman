// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import { TerminalPane } from './TerminalPane';

const mocks = vi.hoisted(() => ({
	terminals: [] as Array<{
		cols: number;
		rows: number;
		loadAddon: ReturnType<typeof vi.fn>;
		open: ReturnType<typeof vi.fn>;
		focus: ReturnType<typeof vi.fn>;
		write: ReturnType<typeof vi.fn>;
		onData: ReturnType<typeof vi.fn>;
		attachCustomKeyEventHandler: ReturnType<typeof vi.fn>;
		parser: { registerOscHandler: ReturnType<typeof vi.fn> };
		dispose: ReturnType<typeof vi.fn>;
	}>,
	fits: [] as Array<{ fit: ReturnType<typeof vi.fn> }>,
	copy: vi.fn(),
}));

vi.mock('../lib/clipboard', () => ({ copyToClipboard: mocks.copy }));

vi.mock('@xterm/xterm', () => ({
	Terminal: class {
		cols = 80;
		rows = 24;
		loadAddon = vi.fn();
		open = vi.fn((el: HTMLElement) => { el.tabIndex = -1; el.focus(); });
		focus = vi.fn();
		write = vi.fn();
		onData = vi.fn(() => ({ dispose: vi.fn() }));
		attachCustomKeyEventHandler = vi.fn();
		parser = { registerOscHandler: vi.fn(() => ({ dispose: vi.fn() })) };
		dispose = vi.fn();
		constructor() { mocks.terminals.push(this); }
	},
}));

vi.mock('@xterm/addon-fit', () => ({
	FitAddon: class {
		fit = vi.fn();
		constructor() { mocks.fits.push(this); }
	},
}));

class FakeWebSocket {
	static OPEN = 1;
	static instances: FakeWebSocket[] = [];
	readonly url: string;
	readyState = FakeWebSocket.OPEN;
	binaryType = '';
	onopen?: () => void;
	onmessage?: (event: MessageEvent) => void;
	onclose?: () => void;
	onerror?: () => void;
	send = vi.fn();
	close = vi.fn();
	constructor(url: string) { this.url = url; FakeWebSocket.instances.push(this); }
}

let resize: (() => void) | undefined;
const disconnect = vi.fn();

beforeEach(() => {
	mocks.terminals.length = 0;
	mocks.fits.length = 0;
	FakeWebSocket.instances.length = 0;
	resize = undefined;
	disconnect.mockReset();
	mocks.copy.mockReset().mockResolvedValue(true);
	vi.stubGlobal('WebSocket', FakeWebSocket);
	vi.stubGlobal('ResizeObserver', class {
		constructor(callback: () => void) { resize = callback; }
		observe = vi.fn();
		disconnect = disconnect;
	});
});

afterEach(() => vi.unstubAllGlobals());

it('copies tmux OSC 52 text to the browser clipboard without sending clipboard reads to the host', async () => {
	const view = render(<TerminalPane dir="/repo" remoteId="remote-1" />);
	const term = mocks.terminals[0];
	expect(term.parser.registerOscHandler).toHaveBeenCalledWith(52, expect.any(Function));
	const handler = term.parser.registerOscHandler.mock.calls[0][1] as (data: string) => boolean;
	await act(async () => { expect(handler(';aMOpbGxv')).toBe(true); });
	expect(mocks.copy).toHaveBeenCalledWith('héllo');
	mocks.copy.mockClear();
	for (const data of ['c;?', 'c;%%%bad', 'c;/w==', 'invalid', 'c;', 'p;?']) {
		await act(async () => { expect(handler(data)).toBe(true); });
	}
	expect(mocks.copy).not.toHaveBeenCalled();
	expect(FakeWebSocket.instances[0].send).not.toHaveBeenCalled();
	view.unmount();
	expect(term.parser.registerOscHandler.mock.results[0].value.dispose).toHaveBeenCalled();
});

it('offers a user-initiated copy when the browser blocks automatic copying', async () => {
	mocks.copy.mockResolvedValueOnce(false).mockResolvedValueOnce(false);
	render(<TerminalPane dir="/repo" />);
	const term = mocks.terminals[0];
	expect(term.parser.registerOscHandler).toHaveBeenCalledWith(52, expect.any(Function));
	const handler = term.parser.registerOscHandler.mock.calls[0][1] as (data: string) => boolean;
	await act(async () => { handler('c;aGVsbG8='); });
	fireEvent.click(screen.getByRole('button', { name: 'Copy to clipboard' }));
	await waitFor(() => expect(mocks.copy).toHaveBeenCalledTimes(2));
	expect(screen.getByRole('button', { name: 'Copy to clipboard' })).toBeInTheDocument();
	fireEvent.click(screen.getByRole('button', { name: 'Copy to clipboard' }));
	await waitFor(() => expect(screen.queryByRole('button', { name: 'Copy to clipboard' })).not.toBeInTheDocument());
	expect(mocks.copy).toHaveBeenNthCalledWith(2, 'hello');
});

it('requires a click for an unfocused terminal and ignores copies in readonly terminals', async () => {
	const view = render(<TerminalPane dir="/repo" />);
	const term = mocks.terminals[0];
	const handler = term.parser.registerOscHandler.mock.calls[0][1] as (data: string) => boolean;
	(document.activeElement as HTMLElement).blur();
	await act(async () => { handler('c;aGVsbG8='); });
	expect(mocks.copy).not.toHaveBeenCalled();
	expect(screen.getByRole('button', { name: 'Copy to clipboard' })).toBeInTheDocument();
	view.unmount();
	render(<TerminalPane dir="/repo" readonly />);
	const readonlyHandler = mocks.terminals[1].parser.registerOscHandler.mock.calls[0][1];
	await act(async () => { readonlyHandler('c;aGVsbG8='); });
	expect(mocks.copy).not.toHaveBeenCalled();
	expect(screen.queryByRole('button', { name: 'Copy to clipboard' })).not.toBeInTheDocument();
});

it('keeps a newer copy available when an earlier clipboard write finishes', async () => {
	let finish!: (ok: boolean) => void;
	mocks.copy.mockImplementationOnce(() => new Promise<boolean>((resolve) => { finish = resolve; }));
	mocks.copy.mockResolvedValueOnce(false);
	const view = render(<TerminalPane dir="/repo" />);
	const handler = mocks.terminals[0].parser.registerOscHandler.mock.calls[0][1];
	await act(async () => { handler('c;b2xk'); handler('c;bmV3'); });
	await act(async () => { finish(true); });
	fireEvent.click(screen.getByRole('button', { name: 'Copy to clipboard' }));
	await waitFor(() => expect(mocks.copy).toHaveBeenLastCalledWith('new'));
	view.unmount();
});

it('ignores completion of a clipboard write after unmount', async () => {
	let finish!: (ok: boolean) => void;
	mocks.copy.mockImplementationOnce(() => new Promise<boolean>((resolve) => { finish = resolve; }));
	const view = render(<TerminalPane dir="/repo" />);
	const handler = mocks.terminals[0].parser.registerOscHandler.mock.calls[0][1];
	await act(async () => { handler('c;aGVsbG8='); });
	view.unmount();
	await act(async () => { finish(true); });
	expect(screen.queryByRole('button', { name: 'Copy to clipboard' })).not.toBeInTheDocument();
});

it('restores terminal focus after the legacy clipboard fallback removes its textarea', async () => {
	const focused = vi.spyOn(document, 'hasFocus').mockReturnValue(true);
	mocks.copy.mockImplementationOnce(async () => {
		(document.activeElement as HTMLElement).blur();
		return true;
	});
	render(<TerminalPane dir="/repo" />);
	const term = mocks.terminals[0];
	await act(async () => { term.parser.registerOscHandler.mock.calls[0][1]('c;aGVsbG8='); });
	expect(term.focus).toHaveBeenCalled();
	focused.mockRestore();
});

it('connects an interactive remote terminal and forwards terminal traffic', async () => {
	const view = render(<TerminalPane dir="/repo path" window="shell" remoteId="remote-1" />);
	const ws = FakeWebSocket.instances[0];
	const term = mocks.terminals[0];

	expect(ws.url).toContain('/api/term/ws?dir=%2Frepo+path&window=shell&remoteId=remote-1');
	expect(ws.binaryType).toBe('arraybuffer');
	expect(screen.getByTestId('terminal-pane')).toBeInTheDocument();
	act(() => ws.onopen?.());
	await waitFor(() => expect(term.focus).toHaveBeenCalled());
	expect(ws.send).toHaveBeenCalledWith(JSON.stringify({ type: 'resize', cols: 80, rows: 24 }));

	act(() => ws.onmessage?.(new MessageEvent('message', { data: 'hello' })));
	expect(term.write).toHaveBeenCalledWith('hello');
	const bytes = new Uint8Array([1, 2]).buffer;
	act(() => ws.onmessage?.(new MessageEvent('message', { data: bytes })));
	expect(term.write).toHaveBeenCalledWith(new Uint8Array(bytes));

	const onData = term.onData.mock.calls[0][0] as (data: string) => void;
	onData('ls\r');
	expect(ws.send).toHaveBeenCalledWith('ls\r');
	const onKey = term.attachCustomKeyEventHandler.mock.calls[0][0] as (event: KeyboardEvent) => boolean;
	const ctrlC = new KeyboardEvent('keydown', { key: 'c', ctrlKey: true, cancelable: true });
	expect(onKey(ctrlC)).toBe(false);
	expect(ctrlC.defaultPrevented).toBe(true);
	expect(ws.send).toHaveBeenCalledWith('\x03');
	expect(onKey(new KeyboardEvent('keyup', { key: 'c', ctrlKey: true }))).toBe(true);
	expect(onKey(new KeyboardEvent('keydown', { key: 'z', ctrlKey: true }))).toBe(true);

	act(() => resize?.());
	expect(mocks.fits[0].fit).toHaveBeenCalledTimes(3);
	view.unmount();
	expect(disconnect).toHaveBeenCalled();
	expect(ws.close).toHaveBeenCalled();
	expect(term.dispose).toHaveBeenCalled();
});

it('keeps readonly terminals passive and reports disconnects and errors', async () => {
	const view = render(<TerminalPane dir="/repo" readonly remoteId="local" />);
	const ws = FakeWebSocket.instances[0];
	const term = mocks.terminals[0];

	expect(ws.url).toContain('/api/term/ws?dir=%2Frepo&readonly=1');
	expect(ws.url).not.toContain('remoteId');
	act(() => ws.onopen?.());
	expect(term.focus).not.toHaveBeenCalled();
	expect(term.onData).not.toHaveBeenCalled();
	expect(term.attachCustomKeyEventHandler).not.toHaveBeenCalled();
	act(() => ws.onclose?.());
	expect(await screen.findByTestId('terminal-status')).toHaveTextContent('Disconnected');
	view.unmount();

	render(<TerminalPane dir="/repo" />);
	const failed = FakeWebSocket.instances[1];
	act(() => failed.onerror?.());
	expect(await screen.findByTestId('terminal-status')).toHaveTextContent('Connection failed');
	act(() => failed.onclose?.());
	expect(screen.getByTestId('terminal-status')).toHaveTextContent('Connection failed');
});
