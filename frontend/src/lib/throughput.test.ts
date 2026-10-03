import { expect, it } from 'vitest';
import type { Message, Part, PartData } from './api';
import { estimateThroughput, throughputSample } from './throughput';

const message = (id: string, created = 1000, completed: number | undefined = 11000, output = 200): Message => ({
  id, sessionId: 's', timeCreated: created,
  data: { role: 'assistant', time: { created, completed }, tokens: { input: 0, output } },
});
const tool = (start?: number, end?: number): PartData => ({ type: 'tool', state: { time: { start, end } } });

it.each([
  ['plain', [], 10000],
  ['overlap', [tool(5000, 11000), tool(3000, 9000)], 2000],
  ['disjoint', [tool(2000, 3000), tool(5000, 6000)], 8000],
  ['clipped', [tool(100, 2000), tool(10000, 15000)], 8000],
  ['outside', [tool(100, 500), tool(12000, 15000)], 10000],
  ['missing start', [tool(undefined, 5000)], 0],
  ['unfinished', [tool(2000)], 0],
  ['reversed', [tool(5000, 2000)], 0],
  ['invalid', [tool(NaN, Infinity)], 0],
  ['all wait', [tool(1000, 11000)], 0],
] as [string, PartData[], number][])('%s timing', (_name, tools, duration) => {
  expect(throughputSample(message('m'), tools)).toEqual(duration ? [200, duration] : [0, 0]);
});

it('omits unfinished, zero-output and unavailable samples', () => {
  const incomplete = message('m');
  delete incomplete.data.time!.completed;
  expect(throughputSample(incomplete, [])).toEqual([0, 0]);
  expect(throughputSample(message('m', 1000, 11000, 0), [])).toEqual([0, 0]);
  expect(throughputSample(message('m', 1000, 1050), [])).toEqual([0, 0]);
  const missingTools = message('m');
  missingTools.data.finish = 'tool-calls';
  expect(throughputSample(missingTools, [])).toEqual([0, 0]);
  expect(estimateThroughput([], [])).toBeNull();
});

it('includes completed subagent samples once and only for the current turn', () => {
  const user: Message = { id: 'u', sessionId: 's', timeCreated: 500, data: { role: 'user' } };
  const parent = message('parent');
  const data: PartData = { ...tool(3000, 11000), tool: 'task' };
  data.state!.output = { task_id: 'child' };
  const parts: Part[] = [
    { id: 'p1', sessionId: 's', messageId: 'parent', data },
    { id: 'p2', sessionId: 's', messageId: 'parent', data: JSON.stringify(data) },
    { id: 'bad', sessionId: 's', messageId: 'parent', data: '{bad' },
  ];
  const child = message('child-m', 4000, 7000, 600);
  const stale = message('old-child', 100, 400, 10000);
  const tasks = { child: { messages: [stale, child], parts: [] }, old: { messages: [message('old')], parts: [] } };
  expect(estimateThroughput([message('old-parent'), user, parent], parts, tasks)).toBe(160);
  // After the next prompt, old parent/task samples must not leak forward.
  expect(estimateThroughput([parent, user], parts, tasks)).toBeNull();
});
