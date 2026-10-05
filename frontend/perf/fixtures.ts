/**
 * Deterministic large fixtures for the interaction bench (`pnpm perf`).
 * No randomness: two runs (or two branches) see identical data.
 */

export const NOW = 1_780_000_000_000;
export const DIR = '/home/user/projects/bench';

export interface Fixture {
  session: Record<string, unknown>;
  messages: Record<string, unknown>[];
  parts: Record<string, unknown>[];
}

export function sessionRow(i: number, over: Record<string, unknown> = {}) {
  return {
    id: `ses_bench${String(i).padStart(4, '0')}`,
    platform: 'opencode',
    projectId: 'p1',
    title: `Bench session ${i}: refactor module ${i % 17}`,
    directory: i < 8 ? DIR : `/home/user/projects/repo-${i % 12}`,
    status: 'done',
    messageCount: 150,
    durationMs: 600_000,
    timeCreated: NOW - (i + 1) * 3_600_000,
    timeUpdated: NOW - i * 60_000,
    summaryAdditions: 10,
    summaryDeletions: 3,
    summaryFiles: 2,
    shareUrl: null,
    seen: true,
    archived: false,
    pinned: false,
    pinnedAt: 0,
    liveConnection: true,
    pendingPermission: false,
    pendingQuestion: false,
    ...over,
  };
}

/** ~200 sidebar rows; the first few share DIR so they group together. */
export const SIDEBAR = Array.from({ length: 200 }, (_, i) => sessionRow(i));

const CODE_TS = [
  '```ts',
  'export function reduce(state: State, action: Action): State {',
  '  switch (action.type) {',
  "    case 'load': return { ...state, items: action.items };",
  "    case 'append': return { ...state, items: [...state.items, action.item] };",
  '    default: return state;',
  '  }',
  '}',
  '```',
].join('\n');

const CODE_GO = [
  '```go',
  'func (s *Server) handle(w http.ResponseWriter, r *http.Request) {',
  '\tctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)',
  '\tdefer cancel()',
  '\tif err := s.store.Save(ctx, r.Body); err != nil {',
  '\t\thttp.Error(w, err.Error(), http.StatusInternalServerError)',
  '\t}',
  '}',
  '```',
].join('\n');

/** Markdown paragraph block `i`, mixing prose, lists, tables and code. */
function block(i: number): string {
  switch (i % 5) {
    case 0: return `## Step ${i}\n\nThe reducer copies the parts array on every delta, so a long stream does O(n) work per token. We batch deltas per frame and **coalesce** writes to the same part; see \`sessionReducer.ts\` and [the spec](https://example.com/spec/${i}).`;
    case 1: return `- check \`useSession\` seeds from cache\n- verify scroll anchoring holds\n- confirm drafts survive a switch\n  - nested item ${i}\n- run \`make test\``;
    case 2: return CODE_TS;
    case 3: return `| file | added | removed |\n| --- | ---: | ---: |\n| a${i}.ts | ${i * 3} | ${i} |\n| b${i}.go | ${i * 2} | ${i + 1} |`;
    default: return CODE_GO;
  }
}

export function markdown(blocks: number, offset = 0): string {
  return Array.from({ length: blocks }, (_, i) => block(i + offset)).join('\n\n');
}

function diffLines(n: number, seed: number) {
  const before = Array.from({ length: n }, (_, i) => `  const value${i} = compute(${i + seed});`).join('\n');
  const after = Array.from({ length: n }, (_, i) => (i % 4 === 0 ? `  const value${i} = computeFast(${i + seed}, cache);` : `  const value${i} = compute(${i + seed});`)).join('\n');
  return { before, after };
}

/**
 * 150 messages: user prompt → assistant (text + bash + read + edit with a
 * 40-line diff). `opts.runningTask` adds a running subagent task part on
 * the last assistant message.
 */
export function buildSession(id: string, title: string, opts: { runningTask?: string } = {}): Fixture {
  const messages: Record<string, unknown>[] = [];
  const parts: Record<string, unknown>[] = [];
  const t0 = NOW - 10_000_000;
  for (let i = 0; i < 150; i++) {
    const mid = `msg_${id}_${String(i).padStart(3, '0')}`;
    const at = t0 + i * 60_000;
    const user = i % 2 === 0;
    messages.push({
      id: mid, sessionId: id, timeCreated: at,
      data: user
        ? { role: 'user', time: { created: at } }
        : { role: 'assistant', finish: 'stop', modelID: 'claude', providerID: 'anthropic', agent: 'build', cost: 0.01, tokens: { input: 1000 + i, output: 400 }, time: { created: at, completed: at + 30_000 } },
    });
    const part = (n: number, data: Record<string, unknown>) =>
      parts.push({ id: `prt_${mid}_${n}`, messageId: mid, sessionId: id, timeCreated: at + n, data });
    if (user) {
      part(0, { type: 'text', text: `Please continue with step ${i}: tighten the reducer and add a test for ${i}.` });
      continue;
    }
    part(0, { type: 'text', text: markdown(3, i) });
    part(1, { type: 'tool', tool: 'bash', callID: `call_${mid}_b`, state: { status: 'completed', input: { command: `go test ./internal/... -run Test${i}`, description: 'Run tests' }, output: Array.from({ length: 20 }, (_, k) => `ok  \tpkg/${k}\t0.${k}s`).join('\n'), time: { start: at, end: at + 2000 } } });
    part(2, { type: 'tool', tool: 'read', callID: `call_${mid}_r`, state: { status: 'completed', input: { filePath: `/src/file${i}.ts` }, output: 'file contents', time: { start: at, end: at + 50 } } });
    const { before, after } = diffLines(40, i);
    part(3, { type: 'tool', tool: 'edit', callID: `call_${mid}_e`, state: { status: 'completed', input: { filePath: `${DIR}/src/module${i}.ts`, oldString: before, newString: after }, output: 'Edit applied', metadata: { filediff: { file: `${DIR}/src/module${i}.ts`, before, after, additions: 10, deletions: 10 } }, time: { start: at, end: at + 100 } } });
  }
  if (opts.runningTask) {
    const last = messages[messages.length - 1];
    parts.push({ id: `prt_${id}_task`, messageId: last.id, sessionId: id, timeCreated: NOW, data: { type: 'tool', tool: 'task', callID: `call_${id}_task`, state: { status: 'running', input: { description: 'Explore the codebase', prompt: 'Find all reducers', subagent_type: 'explore' }, metadata: { sessionId: opts.runningTask }, time: { start: NOW } } } });
  }
  return {
    session: sessionRow(0, { id, title, status: 'done', directory: DIR }),
    messages,
    parts,
  };
}

/** Growing child transcript returned by `/tasks` polls for the subagent. */
export function taskSnapshot(childId: string, step: number) {
  const messages = [];
  const parts = [];
  for (let i = 0; i <= step; i++) {
    const mid = `msg_${childId}_${i}`;
    messages.push({ id: mid, sessionId: childId, timeCreated: NOW + i * 1000, data: { role: 'assistant', time: { created: NOW + i * 1000 } } });
    parts.push({ id: `prt_${mid}`, messageId: mid, sessionId: childId, data: { type: 'tool', tool: 'grep', callID: `c_${mid}`, state: { status: 'completed', input: { pattern: `reducer${i}` }, output: `src/a${i}.ts:12: reducer` } } });
  }
  return { messages, parts };
}

type Frame = [channel: string, data: string][];

/**
 * SSE frames for streaming a long assistant answer into `sessionId`:
 * message.created, a text part snapshot, then one delta per frame.
 * `foreignId` interleaves subagent events (dropped by the reducer but
 * still parsed and dispatched).
 */
export function streamFrames(sessionId: string, opts: { chars?: number; chunk?: number; foreignId?: string } = {}): Frame[] {
  const text = markdown(40, 7).slice(0, opts.chars ?? 9000);
  const chunk = opts.chunk ?? 24;
  const mid = `msg_${sessionId}_live`;
  const pid = `prt_${sessionId}_live`;
  const ev = (type: string, properties: Record<string, unknown>) => JSON.stringify({ type, properties });
  const frames: Frame[] = [[
    ['session.status', ev('session.status', { sessionID: sessionId, status: { type: 'busy' } })],
    ['message.created', ev('message.created', { sessionID: sessionId, info: { id: mid, sessionID: sessionId, role: 'assistant', time: { created: NOW + 1 } }, parts: [] })],
    ['message.part.updated', ev('message.part.updated', { part: { id: pid, messageID: mid, sessionID: sessionId, type: 'text', text: '' } })],
  ]];
  for (let at = 0, n = 0; at < text.length; at += chunk, n++) {
    const frame: Frame = [['message.part.delta', ev('message.part.delta', { sessionID: sessionId, messageID: mid, partID: pid, field: 'text', delta: text.slice(at, at + chunk) })]];
    if (opts.foreignId && n % 3 === 0) {
      frame.push(['message.part.delta', ev('message.part.delta', { sessionID: opts.foreignId, messageID: `m_${opts.foreignId}`, partID: `p_${opts.foreignId}`, field: 'text', delta: 'subagent output ' })]);
    }
    frames.push(frame);
  }
  return frames;
}
