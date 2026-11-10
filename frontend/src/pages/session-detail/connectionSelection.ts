import type { PlatformConnection } from '../../lib/platformConnections';
import { basePlatform, modelRefPlatform } from '../../lib/modelRef';

export function ownerConnectionID(owner: string, id: string) {
  return owner === 'local' ? id : `r-${owner}:${id}`;
}

/** Model references select a kind, never an account. Once named connections
 * are active a model pick can only stay on the explicitly selected account. */
export function connectionForModel(model: string, selected: string | undefined, owner: string, connections: PlatformConnection[] | undefined, legacy: { id: string }[]) {
  const kind = modelRefPlatform(model);
  if (connections) {
    const current = connections.find(c => ownerConnectionID(owner, c.id) === selected);
    return current?.kind === kind ? selected : undefined;
  }
  return legacy.find(p => basePlatform(p.id) === kind)?.id;
}
