import { createHash, randomBytes } from 'node:crypto';
import type { Plugin } from 'vite';

// One identifier for both the loaded JS and the static deployment probe.
// Include a nonce so rebuilding dirty source or the same commit is detectable.
export const buildHash = createHash('sha256').update(randomBytes(32)).digest('hex');

export function buildHashAsset(): Plugin {
  return {
    name: 'ocman-build-hash',
    apply: 'build',
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: 'build-hash.json', source: JSON.stringify({ hash: buildHash }) + '\n' });
    },
  };
}
