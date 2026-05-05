import { describe, it, expect } from 'vitest';
import type { ThreadMessageLike } from '@assistant-ui/react';
import type { Message, Part, PartData } from './api';
import {
  isImageMime,
  parsePart,
  truncate,
  relativizePath,
  computeIsRunning,
  convertMessages,
} from './convertMessages';

function makeMessage(id: string, data: Partial<Message['data']> & { role: 'user' | 'assistant' }, timeCreated = 0): Message {
  return { id, sessionId: 's', timeCreated, data: { ...data } };
}

function makePart(messageId: string, data: PartData, id = ''): Part {
  return {
    id: id || `${messageId}-part-${Math.random().toString(36).slice(2, 8)}`,
    messageId,
    sessionId: 's',
    data: data as unknown as string, // Part.data accepts string|PartData; tests use the object form
  };
}

// Type helpers for content arrays.
type ContentItem =
  | { type: 'text'; text: string }
  | { type: 'image'; image: string }
  | { type: 'tool-call'; toolCallId: string; toolName: string; argsText: string; result?: string };

function asContentArray(content: ThreadMessageLike['content']): ContentItem[] {
  return content as ContentItem[];
}

describe('isImageMime', () => {
  it.each([
    ['image/png', true],
    ['image/jpeg', true],
    ['IMAGE/png', false], // case-sensitive on purpose (the live data is lowercase)
    ['text/plain', false],
    ['', false],
    [undefined, false],
  ])('isImageMime(%j) === %j', (input, expected) => {
    expect(isImageMime(input as string | undefined)).toBe(expected);
  });
});

describe('parsePart', () => {
  it('parses a JSON-string data field once and caches the result', () => {
    const part: Part = {
      id: 'p1',
      messageId: 'm1',
      sessionId: 's',
      data: JSON.stringify({ type: 'text', text: 'hi' }) as unknown as string,
    };
    const first = parsePart(part);
    const second = parsePart(part);
    expect(first).toEqual({ type: 'text', text: 'hi' });
    expect(second).toBe(first); // referentially identical => cache hit
  });

  it('returns the data object as-is when it is already parsed', () => {
    const obj: PartData = { type: 'reasoning', text: 'because' };
    const part = makePart('m1', obj);
    expect(parsePart(part)).toEqual(obj);
  });

  it('returns the raw string when JSON.parse fails (matches original behaviour)', () => {
    // The legacy OcmanRuntimeProvider implementation also surfaced
    // the un-parsed string in this branch — `result = p.data || {}`
    // keeps the string when it is truthy, only falling back to `{}`
    // for empty data. Tests pin that exact behaviour so any
    // intentional change is visible in a diff.
    const part: Part = {
      id: 'p1',
      messageId: 'm1',
      sessionId: 's',
      data: '{not json' as unknown as string,
    };
    expect(parsePart(part)).toBe('{not json' as unknown as PartData);
  });

  it('falls back to {} when data is empty/falsy', () => {
    const part: Part = {
      id: 'p2',
      messageId: 'm1',
      sessionId: 's',
      data: '' as unknown as string,
    };
    // '' is falsy, JSON.parse throws on '', and `'' || {}` => {}
    expect(parsePart(part)).toEqual({});
  });
});

describe('truncate', () => {
  it('returns the empty string for falsy inputs', () => {
    expect(truncate('', 10)).toBe('');
    expect(truncate(null, 10)).toBe('');
    expect(truncate(undefined, 10)).toBe('');
  });

  it('returns short strings unchanged', () => {
    expect(truncate('hi', 10)).toBe('hi');
  });

  it('truncates with the length marker', () => {
    const long = 'x'.repeat(20);
    const out = truncate(long, 5);
    expect(out).toBe('xxxxx\n... (20 chars total)');
  });
});

describe('relativizePath', () => {
  it('returns the input unchanged when projectDir is empty', () => {
    expect(relativizePath('/a/b', '')).toBe('/a/b');
  });

  it('strips the project prefix when the path is under projectDir', () => {
    expect(relativizePath('/repo/internal/db/foo.go', '/repo')).toBe('internal/db/foo.go');
  });

  it('handles a trailing slash on projectDir', () => {
    expect(relativizePath('/repo/foo.go', '/repo/')).toBe('foo.go');
  });

  it('returns "." for the project root itself', () => {
    expect(relativizePath('/repo', '/repo')).toBe('.');
  });

  it('does not match a sibling with a similar prefix', () => {
    // /repo/barn/x must not match the projectDir /repo/bar
    expect(relativizePath('/repo/barn/x', '/repo/bar')).toBe('/repo/barn/x');
  });

  it('returns the absolute path for files outside the project', () => {
    expect(relativizePath('/etc/hosts', '/repo')).toBe('/etc/hosts');
  });

  it('returns the input unchanged when absPath is empty', () => {
    expect(relativizePath('', '/repo')).toBe('');
  });
});

describe('computeIsRunning', () => {
  it('returns false for an empty array', () => {
    expect(computeIsRunning([])).toBe(false);
  });

  it('returns true when the last message is from the user', () => {
    expect(computeIsRunning([makeMessage('m', { role: 'user' })])).toBe(true);
  });

  it('returns true when the last assistant message has no finish reason', () => {
    expect(computeIsRunning([makeMessage('m', { role: 'assistant' })])).toBe(true);
  });

  it('returns false when the last assistant message has finished', () => {
    expect(computeIsRunning([makeMessage('m', { role: 'assistant', finish: 'stop' })])).toBe(false);
  });

  it('returns false when the last assistant message has an error', () => {
    expect(computeIsRunning([makeMessage('m', { role: 'assistant', error: { name: 'x' } })])).toBe(false);
  });
});

describe('convertMessages', () => {
  it('returns an empty array for empty input', () => {
    expect(convertMessages([], [])).toEqual([]);
  });

  it('drops non-user/assistant messages', () => {
    const messages: Message[] = [
      { id: 'sys', sessionId: 's', timeCreated: 0, data: { role: 'system' } },
      makeMessage('u', { role: 'user' }),
    ];
    const out = convertMessages(messages, []);
    expect(out).toHaveLength(1);
    expect(out[0].id).toBe('u');
  });

  it('returns plain string content for a text-only message', () => {
    const m = makeMessage('m', { role: 'user' });
    const p = makePart('m', { type: 'text', text: 'hello' });
    const out = convertMessages([m], [p]);
    expect(out[0].content).toBe('hello');
  });

  it('joins multiple text parts with double newlines', () => {
    const m = makeMessage('m', { role: 'user' });
    const out = convertMessages([m], [
      makePart('m', { type: 'text', text: 'one' }),
      makePart('m', { type: 'text', text: 'two' }),
    ]);
    expect(out[0].content).toBe('one\n\ntwo');
  });

  it('renders reasoning parts as a Markdown blockquote', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages(
      [m],
      [makePart('m', { type: 'reasoning', text: 'thinking…' })],
    );
    expect(out[0].content).toBe('> *Reasoning:* thinking…');
  });

  it('renders patch parts as a fenced diff block', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', { type: 'patch', file: 'a.go', diff: '- old\n+ new' }),
    ]);
    expect(out[0].content).toBe('**a.go**\n```diff\n- old\n+ new\n```');
  });

  it('renders image file parts inline', () => {
    const m = makeMessage('m', { role: 'user' });
    const out = convertMessages([m], [
      makePart('m', { type: 'file', mime: 'image/png', url: 'http://x/a.png' }),
    ]);
    const items = asContentArray(out[0].content);
    expect(items).toContainEqual({ type: 'image', image: 'http://x/a.png' });
  });

  it('renders non-image file parts as a text label', () => {
    const m = makeMessage('m', { role: 'user' });
    const out = convertMessages([m], [
      makePart('m', { type: 'file', mime: 'application/pdf', url: 'u', filename: 'doc.pdf' }),
    ]);
    expect(out[0].content).toBe('📎 doc.pdf (application/pdf)');
  });

  it('skips lifecycle parts (step-start / step-finish / snapshot)', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', { type: 'step-start' } as PartData),
      makePart('m', { type: 'step-finish' } as PartData),
      makePart('m', { type: 'snapshot' } as PartData),
      makePart('m', { type: 'text', text: 'ok' }),
    ]);
    expect(out[0].content).toBe('ok');
  });

  it('renders the read tool as a __read__ tool-call with relativised path', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages(
      [m],
      [makePart('m', {
        type: 'tool',
        tool: 'read',
        state: { input: { filePath: '/repo/src/x.ts' } },
      } as PartData)],
      undefined, undefined, '/repo',
    );
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call' && (i as { toolName: string }).toolName === '__read__');
    expect(tc).toBeDefined();
    expect((tc as { argsText: string }).argsText).toBe('Read src/x.ts');
  });

  it('renders read offset/limit suffix', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages(
      [m],
      [makePart('m', {
        type: 'tool',
        tool: 'read',
        state: { input: { filePath: 'a.txt', offset: '10', limit: '20' } },
      } as PartData)],
    );
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call');
    expect((tc as { argsText: string }).argsText).toBe('Read a.txt [offset=10, limit=20]');
  });

  it('renders grep / glob / webfetch as muted __read__ tool-calls', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', { type: 'tool', tool: 'grep', state: { input: { pattern: 'foo', include: '*.ts' } } } as PartData),
      makePart('m', { type: 'tool', tool: 'glob', state: { input: { pattern: '**/*.go', path: 'internal' } } } as PartData),
      makePart('m', { type: 'tool', tool: 'webfetch', state: { input: { url: 'https://e.com' } } } as PartData),
    ]);
    const items = asContentArray(out[0].content);
    const args = items.filter((i) => i.type === 'tool-call').map((i) => (i as { argsText: string }).argsText);
    expect(args).toEqual([
      'Grep foo (*.ts)',
      'Glob **/*.go (internal)',
      'Fetch https://e.com',
    ]);
  });

  it('renders skill calls as __skill__ tool-calls', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', { type: 'tool', tool: 'Skill', state: { input: { name: 'create-commit' } } } as PartData),
    ]);
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call' && (i as { toolName: string }).toolName === '__skill__');
    expect((tc as { argsText: string }).argsText).toBe('Skill "create-commit"');
  });

  it('renders task calls with subagent metadata + live preview', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages(
      [m],
      [makePart('m', {
        type: 'tool',
        tool: 'task',
        state: {
          status: 'running',
          input: { description: 'do thing', subagent_type: 'build', task_id: 'ses_live_1' },
        },
      } as PartData)],
      undefined,
      { ses_live_1: 'line1\nline2\nline3' },
    );
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call' && (i as { toolName: string }).toolName === '__task__') as
      | { argsText: string; result?: string }
      | undefined;
    expect(tc).toBeDefined();
    expect(tc!.argsText).toBe('running\ndo thing (build)');
    const parsedResult = JSON.parse(tc!.result as string) as { taskId: string; livePreview: string };
    expect(parsedResult.taskId).toBe('ses_live_1');
    expect(parsedResult.livePreview).toContain('line3');
  });

  it('renders question calls as __question__ tool-calls', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', {
        type: 'tool',
        tool: 'question',
        state: {
          status: 'running',
          input: { questions: [{ header: 'h', question: 'q', options: [] }] },
        },
      } as PartData),
    ]);
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call' && (i as { toolName: string }).toolName === '__question__');
    expect(tc).toBeDefined();
  });

  it('renders write tool with a synthetic full-addition diff', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', {
        type: 'tool',
        tool: 'write',
        state: { input: { filePath: 'a.txt', content: 'one\ntwo' } },
      } as PartData),
    ]);
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call' && (i as { toolName: string }).toolName === 'write') as
      | { argsText: string; result?: string }
      | undefined;
    expect(tc).toBeDefined();
    expect(tc!.argsText).toMatch(/Write a\.txt/);
    expect(tc!.result).toContain('+ one');
    expect(tc!.result).toContain('+ two');
  });

  it('renders edit tool using oldString/newString fallback diff', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', {
        type: 'tool',
        tool: 'edit',
        state: { input: { filePath: 'a.go', oldString: 'foo', newString: 'bar' }, output: '' },
      } as PartData),
    ]);
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call' && (i as { toolName: string }).toolName === 'edit') as
      | { argsText: string; result?: string }
      | undefined;
    expect(tc).toBeDefined();
    expect(tc!.argsText).toMatch(/Edit a\.go/);
    expect(tc!.result).toBeTruthy();
  });

  it('renders unknown tool types via the default branch', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], [
      makePart('m', {
        type: 'custom-thing',
        tool: 'mystery',
        state: { status: 'done', input: { command: 'echo hi' }, output: 'done' },
      } as unknown as PartData),
    ]);
    const items = asContentArray(out[0].content);
    const tc = items.find((i) => i.type === 'tool-call');
    expect((tc as { toolName: string }).toolName).toBe('mystery');
    expect((tc as { argsText: string }).argsText).toContain('echo hi');
  });

  it('omits tool calls from user messages (assistant-ui restriction)', () => {
    const m = makeMessage('u', { role: 'user' });
    const out = convertMessages([m], [
      makePart('u', { type: 'tool', tool: 'read', state: { input: { filePath: 'a' } } } as PartData),
      makePart('u', { type: 'text', text: 'hi' }),
    ]);
    expect(out[0].content).toBe('hi');
  });

  it('flags queued user messages with metadata.custom.queued', () => {
    const messages: Message[] = [
      makeMessage('u1', { role: 'user' }, 1),
      makeMessage('a1', { role: 'assistant' }, 2), // unfinished
      makeMessage('u2', { role: 'user' }, 3),
    ];
    const out = convertMessages(messages, []);
    const u2 = out.find((m) => m.id === 'u2');
    expect((u2?.metadata?.custom as { queued?: boolean })?.queued).toBe(true);
  });

  it('does NOT flag the first user message as queued (no prior user turn)', () => {
    const messages: Message[] = [
      makeMessage('a1', { role: 'assistant' }, 1), // bootstrap
      makeMessage('u1', { role: 'user' }, 2),
    ];
    const out = convertMessages(messages, []);
    expect((out[1].metadata?.custom as { queued?: boolean } | undefined)?.queued).toBeUndefined();
  });

  it('resolves msgAgent for a user message from the next assistant reply', () => {
    const messages: Message[] = [
      makeMessage('u', { role: 'user' }, 1),
      makeMessage('a', { role: 'assistant', agent: 'plan' }, 2),
    ];
    const out = convertMessages(messages, []);
    expect((out[0].metadata?.custom as { agent?: string })?.agent).toBe('plan');
  });

  it('falls back to pendingAgent when there is no next assistant reply', () => {
    const out = convertMessages(
      [makeMessage('u', { role: 'user' }, 1)],
      [],
      'build',
    );
    expect((out[0].metadata?.custom as { agent?: string })?.agent).toBe('build');
  });

  it('produces deeply-equal output on repeated identical calls', () => {
    // The implementation has a per-message WeakMap cache, but it is
    // keyed on the freshly-bucketed parts array reference (which is
    // rebuilt every call), so referential identity does not hold in
    // the general case. The contract we *can* test is that the
    // result is deeply identical for identical inputs — i.e. the
    // function is deterministic.
    const m = makeMessage('m', { role: 'user' });
    const parts = [makePart('m', { type: 'text', text: 'hello' })];
    const first = convertMessages([m], parts);
    const second = convertMessages([m], parts);
    expect(second[0]).toEqual(first[0]);
  });

  it('produces a different result when pendingAgent changes', () => {
    const m = makeMessage('u', { role: 'user' }, 1);
    const first = convertMessages([m], [], 'plan');
    const second = convertMessages([m], [], 'build');
    expect((first[0].metadata?.custom as { agent?: string })?.agent).toBe('plan');
    expect((second[0].metadata?.custom as { agent?: string })?.agent).toBe('build');
  });

  it('marks failed user messages via metadata.custom.failed', () => {
    const m = makeMessage('u', { role: 'user' }, 1);
    const failedById = { u: { id: 'u', error: 'boom', text: '', images: [], imagesDropped: true, failedAt: 0 } };
    const out = convertMessages([m], [], undefined, undefined, undefined, failedById);
    expect((out[0].metadata?.custom as { failed?: { error: string; imagesDropped: boolean } })?.failed).toEqual({
      error: 'boom',
      imagesDropped: true,
    });
  });

  it('renders error text on assistant messages (skipping abort errors)', () => {
    const m = makeMessage('m', { role: 'assistant', error: { name: 'BadThing', data: { message: 'oops' } } });
    const out = convertMessages([m], []);
    expect(out[0].content).toContain('**BadThing:** oops');

    const aborted = makeMessage('m', { role: 'assistant', error: { name: 'AbortError', data: {} } });
    const out2 = convertMessages([aborted], []);
    expect(out2[0].content).toBe('');
  });

  it('sets msgStatus to running for in-flight assistant messages', () => {
    const m = makeMessage('m', { role: 'assistant' });
    const out = convertMessages([m], []);
    expect(out[0].status).toEqual({ type: 'running' });
  });

  it('sets msgStatus to incomplete for errored assistant messages', () => {
    const m = makeMessage('m', { role: 'assistant', finish: 'error' });
    const out = convertMessages([m], []);
    expect(out[0].status).toEqual({ type: 'incomplete', reason: 'error' });
  });

  it('sets msgStatus to complete when a finish reason is set', () => {
    const m = makeMessage('m', { role: 'assistant', finish: 'stop' });
    const out = convertMessages([m], []);
    expect(out[0].status).toEqual({ type: 'complete', reason: 'stop' });
  });
});
