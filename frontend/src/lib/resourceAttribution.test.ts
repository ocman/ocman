import { expect, it } from 'vitest';
import { attributeResourceHistory } from './resourceAttribution';
import type { ResourceSnapshot } from './resources';

it('uses current attribution without changing historical metrics or the raw input', () => {
  const raw = { pid: 2, parentPid: 1, name: 'agent', cpuPercent: 25, memoryBytes: 100 };
  const snapshot: ResourceSnapshot = {
    timestamp: 2000, cpuPercent: 50, memoryBytes: 200,
    processes: [{ ...raw, cpuPercent: 50, memoryBytes: 200, sessionId: 'one', sessionTitle: 'Current title', projectName: 'ocman' }],
    history: [{ timestamp: 1000, cpuPercent: 25, memoryBytes: 100, processes: [raw] }],
  };
  const result = attributeResourceHistory(snapshot);
  expect(result.history[0].processes).toEqual([{ ...raw, sessionId: 'one', sessionTitle: 'Current title', projectName: 'ocman' }]);
  expect(snapshot.history[0].processes).toEqual([raw]);
  expect(result.history[0].cpuPercent).toBe(25);
  expect(result.processes).toBe(snapshot.processes);
});

it('drops expired history and does not match processes by PID alone', () => {
  const process = { pid: 2, parentPid: 1, name: 'agent', cpuPercent: 25, memoryBytes: 100 };
  const previous: ResourceSnapshot = {
    timestamp: 1000, cpuPercent: 25, memoryBytes: 100, processes: [],
    history: [{ timestamp: 1000, cpuPercent: 25, memoryBytes: 100, processes: [{ ...process, sessionId: 'one' }] }],
  };
  const next: ResourceSnapshot = {
    timestamp: 2000, cpuPercent: 0, memoryBytes: 0, processes: [],
    history: [{ timestamp: 2000, cpuPercent: 0, memoryBytes: 0, processes: [{ ...process, parentPid: 3 }] }],
  };
  expect(attributeResourceHistory(next, previous)).toEqual(next);
  const empty = { ...next, history: [{ timestamp: 3000, cpuPercent: 0, memoryBytes: 0 }] };
  expect(attributeResourceHistory(empty, previous)).toEqual(empty);
});
