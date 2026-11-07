import { describe, expect, it } from 'vitest';
import { durableStartRequest, type Ready } from './newConversationSubmission';

const ready = { catalog: { defaults: { worktree: 'current' } }, canWorktree: true, worktrees: [], platform: 'opencode', model: 'prov/m' } as unknown as Ready;
const params = { directory: '/repo', remoteId: 'local', platformForModel: () => 'opencode' };

describe('durableStartRequest', () => {
  it('carries an explicit permission posture to the accepted draft', () => {
    const rules = [{ permission: '*', pattern: '*', action: 'allow' as const }];
    expect(durableStartRequest(ready, { model: 'prov/m' }, { ...params, permissionRules: rules }, 'hi').permissionRules).toEqual(rules);
  });

  it('omits permission rules so the backend applies project defaults', () => {
    expect(durableStartRequest(ready, { model: 'prov/m' }, params, 'hi')).not.toHaveProperty('permissionRules');
  });
});
