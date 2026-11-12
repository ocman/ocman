import type { PlatformConnection } from '../../lib/platformConnections';
import { basePlatform, modelRefPlatform } from '../../lib/modelRef';

export function ownerConnectionID(owner: string, id: string) {
  return owner === 'local' ? id : `r-${owner}:${id}`;
}

/** Explicit named model references select that account; legacy kind references stay on the current account. */
export function connectionForModel(model: string, selected: string | undefined, owner: string, connections: PlatformConnection[] | undefined, legacy: { id: string }[]) {
  const kind = modelRefPlatform(model);
  if (connections) {
    const account = connections.find(c => c.id === kind);
    if (account) return account.enabled && account.status === 'ready' ? ownerConnectionID(owner, account.id) : undefined;
    const current = connections.find(c => ownerConnectionID(owner, c.id) === selected);
    return current?.kind === kind ? selected : undefined;
  }
  return legacy.find(p => basePlatform(p.id) === kind)?.id;
}
