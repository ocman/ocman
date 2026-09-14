// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
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
		dispose: ReturnType<typeof vi.fn>;
	}>,
	fits: [] as Array<{ fit: ReturnType<typeof vi.fn> }>,
}));

vi.mock('@xterm/xterm', () => ({
	Terminal: class {
		cols = 80;
		rows = 24;
		loadAddon = vi.fn();
		open = vi.fn();
		focus = vi.fn();
		write = vi.fn();
		onData = vi.fn(() => ({ dispose: vi.fn() }));
		attachCustomKeyEventHandler = vi.fn();
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
	vi.stubGlobal('WebSocket', FakeWebSocket);
	vi.stubGlobal('ResizeObserver', class {
		constructor(callback: () => void) { resize = callback; }
		observe = vi.fn();
		disconnect = disconnect;
	});
});

afterEach(() => vi.unstubAllGlobals());

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
