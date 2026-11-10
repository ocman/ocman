import type { ConnectionInput } from './platformConnections';

const key = (owner: string) => `ocman.connection-setup.v1:${owner}`;
export function readConnectionSetup(owner: string): ConnectionInput | undefined {
  try {
    const value: unknown = JSON.parse(localStorage.getItem(key(owner)) ?? 'null');
    if (!value || typeof value !== 'object') return;
    const v = value as Partial<ConnectionInput>;
    if (typeof v.reservationId !== 'string' || !v.reservationId.startsWith('stp_') || typeof v.kind !== 'string' || typeof v.name !== 'string' || typeof v.directory !== 'string' || typeof v.enabled !== 'boolean' || !['existing', 'isolated'].includes(v.configuration ?? '')) return;
    return v as ConnectionInput;
  } catch { return; }
}
export function writeConnectionSetup(owner: string, input?: ConnectionInput) {
  // Store only setup choices and the owner receipt, never account observations.
  if (input) {
    const { kind, name, configuration, directory, enabled, reservationId } = input;
    localStorage.setItem(key(owner), JSON.stringify({ kind, name, configuration, directory, enabled, reservationId }));
  }
  else localStorage.removeItem(key(owner));
}
