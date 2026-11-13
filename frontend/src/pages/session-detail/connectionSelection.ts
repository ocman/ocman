import type { PlatformConnection } from '../../lib/platformConnections';
import { basePlatform, modelRefPlatform } from '../../lib/modelRef';

export function ownerConnectionID(owner: string, id: string) {
  return owner === 'local' ? id : `r-${owner}:${id}`;
}

/** Keep same-kind picks on the current account; cross-kind picks require an unambiguous account. */
export function connectionForModel(model: string, selected: string | undefined, owner: string, connections: PlatformConnection[] | undefined, legacy: { id: string }[]) {
  const kind = modelRefPlatform(model);
  if (connections) {
    const account = connections.find(c => c.id === kind);
    if (account) return account.enabled && account.status === 'ready' ? ownerConnectionID(owner, account.id) : undefined;
    const current = connections.find(c => ownerConnectionID(owner, c.id) === selected);
    if (!current) return undefined;
    if (current.kind === kind) return selected;
    const matching = connections.filter(c => c.kind === kind);
    const next = matching.length === 1 ? matching[0] : undefined;
    return next?.enabled && next.status === 'ready' ? ownerConnectionID(owner, next.id) : undefined;
  }
  return legacy.find(p => basePlatform(p.id) === kind)?.id;
}
