import type { ResourceSnapshot } from './resources';
import { processKey } from './resourcePresentation';

// The API enriches current processes, but sampler history contains raw process
// identities. Carry known context into those samples without changing metrics.
// The previous query result retains exited-process context until history expires.
export function attributeResourceHistory(snapshot: ResourceSnapshot, previous?: ResourceSnapshot): ResourceSnapshot {
  const known = new Map([
    ...(previous?.history.flatMap((point) => point.processes ?? []) ?? []),
    ...snapshot.history.flatMap((point) => point.processes ?? []),
    ...snapshot.processes,
  ]
    .filter((process) => process.sessionId || process.projectName)
    .map((process) => [processKey(process), process]));
  return { ...snapshot, history: snapshot.history.map((point) => ({ ...point, processes: point.processes?.map((process) => {
    const context = known.get(processKey(process));
    return context ? { ...process, sessionId: context.sessionId, sessionTitle: context.sessionTitle, projectName: context.projectName } : process;
  }) })) };
}
