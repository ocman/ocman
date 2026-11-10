// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { readConnectionSetup, writeConnectionSetup } from './connectionSetupReceipt';

describe('setup receipt recovery', () => {
  it('recovers the owner-scoped identity without account observations', () => {
    const input = { kind: 'codex', name: 'Work', configuration: 'isolated' as const, directory: '', enabled: true, reservationId: 'stp_saved' };
    writeConnectionSetup('owner-a', input);
    expect(readConnectionSetup('owner-a')).toEqual(input);
    expect(readConnectionSetup('owner-b')).toBeUndefined();
    writeConnectionSetup('owner-a');
    expect(readConnectionSetup('owner-a')).toBeUndefined();
  });
  it('ignores malformed browser storage', () => {
    localStorage.setItem('ocman.connection-setup.v1:bad', '{bad');
    expect(readConnectionSetup('bad')).toBeUndefined();
    localStorage.setItem('ocman.connection-setup.v1:bad', '{"reservationId":"cnx_wrong"}');
    expect(readConnectionSetup('bad')).toBeUndefined();
  });
});
