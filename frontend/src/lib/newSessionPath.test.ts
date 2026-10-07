import { describe, expect, it } from 'vitest';
import { newSessionPath, parseNewSessionParams } from './newSessionPath';

describe('newSessionPath', () => {
  it('gives each prepared conversation its own reusable draft identity', () => {
    const first = newSessionPath({ directory: '/repo' });
    const second = newSessionPath({ directory: '/repo' });
    expect(first).not.toBe(second);
    const params = parseNewSessionParams(new URLSearchParams(first.split('?')[1]))!;
    expect(params).toHaveProperty('draftId');
    expect(newSessionPath(params)).toBe(first);
    expect(newSessionPath({ ...params, remoteId: 'box' })).toContain(`draftId=${params.draftId}`);
  });
  it('round-trips the target through the query string, dropping the local default', () => {
    const path = newSessionPath({ directory: '/src/repo', remoteId: 'local', platform: 'opencode', title: 'Fix login', draftId: 'a' });
    expect(path).toBe('/session/new?dir=%2Fsrc%2Frepo&platform=opencode&title=Fix+login&draftId=a');
    expect(parseNewSessionParams(new URLSearchParams(path.split('?')[1]))).toEqual({
      directory: '/src/repo', remoteId: 'local', platform: 'opencode', title: 'Fix login', draftId: 'a',
    });
    expect(parseNewSessionParams(new URLSearchParams(newSessionPath({ directory: '/r', remoteId: 'box' }).split('?')[1])))
      .toEqual({ directory: '/r', remoteId: 'box', platform: undefined, title: undefined, draftId: expect.any(String) });
  });

  it('is not a new conversation without a directory', () => {
    expect(parseNewSessionParams(new URLSearchParams(''))).toBeNull();
  });
});
