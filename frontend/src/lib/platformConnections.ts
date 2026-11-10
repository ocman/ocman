/** Connection IDs are opaque, stable routing keys, never names or platform types. */
export type PlatformConnection = {
  id: string;
  kind: string;
  name: string;
  configuration: 'existing' | 'isolated';
  directory: string;
  enabled: boolean;
  isDefault: boolean;
  status: 'ready' | 'sign-in-required' | 'dependencies-missing' | 'unchecked' | 'error';
  account?: string;
};

/** Setup choices come from the owner, not platform-identity branches in the UI. */
export type ConnectionPlatform = {
  id: string;
  label: string;
  existingDirectory: string;
  supportsIsolation: boolean;
  isolationNote: string;
};

export type ConnectionInput = Pick<PlatformConnection, 'kind' | 'name' | 'configuration' | 'directory' | 'enabled'> & {
  connectionId?: string;
  /** Owner-issued setup identity. Never replace it with an observed path/account. */
  reservationId?: string;
};

export type ConnectionCheck = {
  reservationId?: string;
  status: PlatformConnection['status'];
  account?: string;
  directory?: string;
  checks: { label: string; ok: boolean; detail: string }[];
  instructions?: string;
  command?: string;
};

export const connectionStatusLabel: Record<PlatformConnection['status'], string> = {
  ready: 'Ready',
  'sign-in-required': 'Sign-in required',
  'dependencies-missing': 'Setup required',
  unchecked: 'Not checked',
  error: 'Check failed',
};

export function connectionAvailabilityLabel(connection: PlatformConnection): string {
  if (connection.enabled) return connectionStatusLabel[connection.status];
  return connection.status === 'error' ? `Disabled · ${connectionStatusLabel.error}` : 'Disabled';
}
