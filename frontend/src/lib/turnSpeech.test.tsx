// @vitest-environment jsdom
import { act, cleanup, renderHook } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { Message, Part, PartData, SessionStatus } from './api';
import { computeTurnStats } from './turnStats';
import { finalAnswers, speechText, useSpeechPlayback, useTurnSpeech } from './turnSpeech';
import { useUiStore } from './uiStore';

const msg = (id: string, data: Message['data']): Message => ({ id, sessionId: 's', timeCreated: 1, data });
const user = msg('u', { role: 'user' });
const answer = msg('a', { role: 'assistant', finish: 'stop', time: { created: 1, completed: 2 } });
const part = (messageId: string, data: PartData): Part => ({ id: `${messageId}-${data.type}`, messageId, sessionId: 's', data });
const text = part('a', { type: 'text', text: '**Finished.**' });
const speak = vi.fn();
const cancel = vi.fn();
const local = { voiceURI: 'local', localService: true, lang: 'en-US' } as SpeechSynthesisVoice;
const online = { voiceURI: 'online', localService: false, lang: 'en-US' } as SpeechSynthesisVoice;
class Utterance {
  text: string;
  onend?: () => void;
  onerror?: (event: { error: string }) => void;
  constructor(text: string) { this.text = text; }
}
beforeEach(() => {
  speak.mockReset(); cancel.mockReset();
  vi.stubGlobal('SpeechSynthesisUtterance', Utterance);
  vi.stubGlobal('speechSynthesis', { speak, cancel, getVoices: () => [online, local] });
  vi.spyOn(document, 'hasFocus').mockReturnValue(true);
  useUiStore.setState({ autoReadAnswers: false, speechVoiceURI: '', speechRate: 1 });
});
afterEach(() => { cleanup(); vi.unstubAllGlobals(); vi.restoreAllMocks(); });

describe('final answer selection', () => {
  it('reads only the terminal assistant text, never progress, reasoning, tools, or synthetic parts', () => {
    const progress = msg('progress', { role: 'assistant', finish: 'tool-calls' });
    const messages = [user, progress, answer];
    const parts = [part('progress', { type: 'text', text: 'Checking...' }),
      part('a', { type: 'reasoning', text: 'Private reasoning' }),
      part('a', { type: 'tool', state: { input: { command: 'rm x' }, output: 'output' } }),
      part('a', { type: 'text', text: 'Generated instruction', synthetic: true }),
      part('a', { type: 'text', text: 'Ignored', ignored: true }), text];
    expect([...finalAnswers(messages, parts, computeTurnStats(messages, parts))]).toEqual([['a', 'Finished.']]);
  });
  it.each([
    { finish: 'tool-calls' }, { finish: 'length' }, { finish: undefined },
    { error: { name: 'AbortError' } }, { summary: true }, { agent: 'compaction' },
  ])('rejects non-final responses: %j', (data) => {
    const messages = [user, { ...answer, data: { ...answer.data, ...data } }];
    expect(finalAnswers(messages, [text], computeTurnStats(messages, [text])).size).toBe(0);
  });
  it('never falls back to earlier text when the last response is empty or code-only', () => {
    const messages = [user, answer, msg('last', { role: 'assistant', finish: 'stop' })];
    const parts = [text, part('last', { type: 'text', text: '```sh\nrm x\n```' })];
    expect(finalAnswers(messages, parts, computeTurnStats(messages, parts)).size).toBe(0);
  });
  it('accepts end_turn and serialized parts, but not live turns', () => {
    const messages = [user, msg('a', { role: 'assistant', finish: 'end_turn' })];
    const parts = [{ ...text, data: JSON.stringify(text.data) }];
    expect(finalAnswers(messages, parts, computeTurnStats(messages, parts)).get('a')).toBe('Finished.');
    expect(finalAnswers(messages, parts, computeTurnStats(messages, parts, true)).size).toBe(0);
  });
});

it('cleans Markdown with its parser, dropping fences, indented code, images, and link URLs', () => {
  expect(speechText('# Done\n\nUse `Widget` &amp; **friends**. [Docs](https://secret.example).\n\n~~~sh\nsecret command\n~~~\n\n    another command\n\n![diagram](https://image.example)\n\n- Works\n- Tested'))
    .toBe('Done Use Widget & friends. Docs. Works Tested');
  expect(speechText('## Two\n### Three\n#### Four\n##### Five\n###### Six\n\n| Name | Status |\n| --- | --- |\n| Tests | Pass |'))
    .toBe('Two Three Four Five Six Name Status Tests Pass');
  expect(speechText('```\nunterminated code')).toBe('');
});

describe('speech playback', () => {
  it('prefers local voices, applies preferences, stops and ignores stale callbacks', () => {
    const { result, unmount } = renderHook(useSpeechPlayback);
    act(() => result.current.play('a', 'Hello'));
    const first = speak.mock.calls[0][0];
    expect(first.voice).toBe(local);
    expect(result.current.speakingId).toBe('a');
    act(() => useUiStore.setState({ speechVoiceURI: 'online', speechRate: 1.5 }));
    act(() => result.current.play('b', 'Next'));
    expect(cancel).toHaveBeenCalledTimes(1);
    expect(speak.mock.calls[1][0]).toMatchObject({ voice: online, rate: 1.5 });
    act(() => { first.onend(); first.onerror({ error: 'network' }); });
    expect(result.current.speakingId).toBe('b');
    expect(result.current.error).toBeNull();
    unmount();
    expect(cancel).toHaveBeenCalledTimes(2);
  });
  it('settles on end and exposes browser errors without treating cancellation as failure', () => {
    const { result } = renderHook(useSpeechPlayback);
    act(() => result.current.play('a', 'Hello'));
    act(() => speak.mock.calls[0][0].onend());
    expect(result.current.speakingId).toBeNull();
    for (const error of ['canceled', 'interrupted', 'not-allowed']) {
      act(() => result.current.play('a', 'Hello'));
      act(() => speak.mock.calls.at(-1)![0].onerror({ error }));
      expect(Boolean(result.current.error)).toBe(error === 'not-allowed');
    }
    speak.mockImplementation(() => { throw new Error('unavailable'); });
    act(() => result.current.play('a', 'Hello'));
    expect(result.current.error).toContain('unavailable');
  });
  it('handles unsupported browsers, empty speech, and voices that are not loaded yet', () => {
    const { result } = renderHook(useSpeechPlayback);
    act(() => result.current.play('a', ' '));
    expect(speak).not.toHaveBeenCalled();
    vi.stubGlobal('speechSynthesis', { speak, cancel, getVoices: () => [] });
    act(() => result.current.play('a', 'Hello'));
    expect(speak.mock.calls[0][0].voice).toBeNull();
    act(() => result.current.stop());
    vi.stubGlobal('speechSynthesis', undefined);
    const unsupported = renderHook(useSpeechPlayback);
    act(() => unsupported.result.current.play('a', 'Hello'));
    expect(unsupported.result.current.supported).toBe(false);
    expect(speak).toHaveBeenCalledTimes(1);
  });
});

type Input = { messages: Message[]; parts: Part[]; status: SessionStatus; connected: boolean; reconciled: boolean; key: string };
const busy: Input = { messages: [user], parts: [], status: 'busy', connected: true, reconciled: true, key: 'local:s' };
const done: Input = { ...busy, messages: [user, answer], parts: [text], status: 'done' };
function setup(initialProps = busy) {
  return renderHook((p: Input) => useTurnSpeech(p.messages, p.parts,
    computeTurnStats(p.messages, p.parts), p.key, p.status, p.connected, p.reconciled), { initialProps });
}
describe('turn playback lifecycle', () => {
  it('is opt-in, allows manual playback, and toggles stop', () => {
    const { result } = setup(done);
    expect(speak).not.toHaveBeenCalled();
    act(() => result.current.toggle('a'));
    expect(speak.mock.calls[0][0].text).toBe('Finished.');
    act(() => result.current.toggle('a'));
    expect(result.current.speakingId).toBeNull();
    act(() => result.current.toggle('missing'));
    expect(speak).toHaveBeenCalledTimes(1);
  });
  it('waits for idle reconciliation, then reads once even after duplicate events', () => {
    useUiStore.setState({ autoReadAnswers: true });
    const { rerender } = setup();
    rerender({ ...done, status: 'busy' });
    expect(speak).not.toHaveBeenCalled();
    rerender({ ...done, reconciled: false });
    expect(speak).not.toHaveBeenCalled();
    rerender(done);
    expect(speak).toHaveBeenCalledTimes(1);
    rerender({ ...done, messages: [...done.messages], parts: [...done.parts] });
    expect(speak).toHaveBeenCalledTimes(1);
  });
  it('does not replay history, reconnect completions, or answers completed in a background tab', () => {
    useUiStore.setState({ autoReadAnswers: true });
    const history = setup(done);
    expect(speak).not.toHaveBeenCalled();
    history.unmount();
    const disconnected = setup();
    disconnected.rerender({ ...busy, connected: false });
    disconnected.rerender(done);
    expect(speak).not.toHaveBeenCalled();
    disconnected.unmount();
    const background = setup();
    vi.spyOn(document, 'hasFocus').mockReturnValue(false);
    background.rerender(done);
    vi.spyOn(document, 'hasFocus').mockReturnValue(true);
    background.rerender({ ...done, parts: [...done.parts] });
    expect(speak).not.toHaveBeenCalled();
  });
  it('does not read errors and cancels playback on new prompts, disabling, and navigation', () => {
    useUiStore.setState({ autoReadAnswers: true });
    const { result, rerender } = setup();
    rerender({ ...done, status: 'error' });
    expect(result.current.answers.size).toBe(0);
    expect(speak).not.toHaveBeenCalled();
    rerender(busy);
    rerender(done);
    expect(result.current.speakingId).toBe('a');
    rerender({ ...done, messages: [...done.messages, msg('u2', { role: 'user' })] });
    expect(result.current.speakingId).toBeNull();
    act(() => result.current.toggle('a'));
    act(() => useUiStore.setState({ autoReadAnswers: false }));
    expect(result.current.speakingId).toBeNull();
    act(() => result.current.toggle('a'));
    rerender({ ...done, key: 'remote:s' });
    expect(result.current.speakingId).toBeNull();
  });
  it('scopes replay protection to the owning platform and session', () => {
    useUiStore.setState({ autoReadAnswers: true });
    const { rerender } = setup();
    rerender(done);
    expect(speak).toHaveBeenCalledTimes(1);
    rerender({ ...busy, key: 'remote:s' });
    rerender({ ...done, key: 'remote:s' });
    expect(speak).toHaveBeenCalledTimes(2);
  });
});
