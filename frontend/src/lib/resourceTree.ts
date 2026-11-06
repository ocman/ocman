import type { ResourceProcess } from './resources';

// Input order defines sibling order. Missing parents become roots; cycles are
// broken at the first unvisited process so every process still appears once.
export function resourceTree(processes: ResourceProcess[]) {
  const byPid = new Map(processes.map((process) => [process.pid, process]));
  const children = new Map<number, ResourceProcess[]>();
  for (const process of processes) {
    const siblings = children.get(process.parentPid) ?? [];
    siblings.push(process);
    children.set(process.parentPid, siblings);
  }
  const rows: { process: ResourceProcess; depth: number }[] = [];
  const visited = new Set<number>();
  const append = (root: ResourceProcess) => {
    const pending = [{ process: root, depth: 0 }];
    while (pending.length) {
      const row = pending.pop()!;
      if (visited.has(row.process.pid)) continue;
      visited.add(row.process.pid);
      rows.push(row);
      for (const child of [...(children.get(row.process.pid) ?? [])].reverse()) {
        pending.push({ process: child, depth: row.depth + 1 });
      }
    }
  };
  processes.filter((process) => !byPid.has(process.parentPid)).forEach(append);
  processes.forEach(append);
  return rows;
}
