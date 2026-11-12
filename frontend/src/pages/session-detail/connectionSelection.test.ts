import { describe, expect, it } from 'vitest';
import { connectionForModel } from './connectionSelection';
import type { PlatformConnection } from '../../lib/platformConnections';

const connections: PlatformConnection[] = ['personal', 'work'].map(name => ({ id: `cnx_${name}`, kind: 'codex', name, configuration: 'isolated', directory: `/${name}`, enabled: true, isDefault: false, status: 'ready' }));

describe('model and connection routing', () => {
  it('honors an explicit named model choice on the selected owner and refuses disabled accounts', () => {
    expect(connectionForModel('cnx_work:openai/gpt', 'cnx_personal', 'local', connections, [])).toBe('cnx_work');
    expect(connectionForModel('cnx_work:openai/gpt', undefined, 'box', connections, [])).toBe('r-box:cnx_work');
    expect(connectionForModel('cnx_work:openai/gpt', undefined, 'box', connections.map(c => ({ ...c, enabled: false })), [])).toBeUndefined();
  });
  it('preserves the selected account even when a sibling appears first', () => {
    expect(connectionForModel('codex:openai/gpt', 'cnx_work', 'local', connections, [])).toBe('cnx_work');
    expect(connectionForModel('codex:openai/gpt', 'r-box:cnx_work', 'box', connections, [])).toBe('r-box:cnx_work');
  });
  it('never invents a sibling default or switches kind in named mode', () => {
    expect(connectionForModel('codex:openai/gpt', undefined, 'local', connections, [])).toBeUndefined();
    expect(connectionForModel('claudecode:anthropic/opus', 'cnx_work', 'local', connections, [{ id: 'claudecode' }])).toBeUndefined();
    expect(connectionForModel('codex:openai/gpt', 'cnx_removed', 'local', connections, [])).toBeUndefined();
  });
  it('retains legacy model-to-platform routing when named connections are unsupported', () => {
    expect(connectionForModel('codex:openai/gpt', 'opencode', 'box', undefined, [{ id: 'r-box:codex' }])).toBe('r-box:codex');
  });
});
