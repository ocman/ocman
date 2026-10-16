// Node broadcasts across worker threads, but each Vitest file has its own
// fake IndexedDB. Keep simulated tabs connected only within their test file.
// This runs before application imports and survives vi.resetModules().
const NativeBroadcastChannel = globalThis.BroadcastChannel;
const namespace = crypto.randomUUID();

globalThis.BroadcastChannel = class extends NativeBroadcastChannel {
  constructor(name: string) {
    super(`${namespace}:${name}`);
  }
};

export {};
