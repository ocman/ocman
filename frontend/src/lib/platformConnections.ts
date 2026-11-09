/** Connection IDs are opaque, stable routing keys, never names or platform types. */
export type PlatformConnection = {
  id: string;
  kind: string;
  name: string;
  configuration: 'existing' | 'isolated';
  directory: string;
  enabled: boolean;
  isDefault: boolean;
  status: 'ready' | 'sign-in-required' | 'dependencies-missing' | 'unchecked';
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

export type ConnectionInput = Pick<PlatformConnection, 'kind' | 'name' | 'configuration' | 'directory' | 'enabled'>;

export type ConnectionCheck = {
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
};
