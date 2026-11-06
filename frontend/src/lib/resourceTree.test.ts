import { expect, it } from 'vitest';
import { resourceTree } from './resourceTree';

const process = (pid: number, parentPid: number) => ({ pid, parentPid, name: `process-${pid}`, cpuPercent: 0, memoryBytes: 0 });

it('keeps parents before children and preserves sibling sorting', () => {
  expect(resourceTree([process(4, 1), process(3, 2), process(2, 1), process(1, 0), process(5, 99)])
    .map(({ process, depth }) => [process.pid, depth])).toEqual([[1, 0], [4, 1], [2, 1], [3, 2], [5, 0]]);
});

it('renders cycles and self-parented processes exactly once', () => {
  expect(resourceTree([process(1, 2), process(2, 1), process(3, 3)])
    .map(({ process, depth }) => [process.pid, depth])).toEqual([[1, 0], [2, 1], [3, 0]]);
  expect(resourceTree([])).toEqual([]);
});

it('handles deep process chains without recursive traversal', () => {
  const rows = resourceTree(Array.from({ length: 10000 }, (_, index) => process(index + 1, index)));
  expect(rows).toHaveLength(10000);
  expect(rows.at(-1)?.depth).toBe(9999);
});
