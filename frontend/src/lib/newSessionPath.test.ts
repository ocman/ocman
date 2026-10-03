import { describe, expect, it } from 'vitest';
import { newSessionPath, parseNewSessionParams } from './newSessionPath';

describe('newSessionPath', () => {
  it('round-trips the target through the query string, dropping the local default', () => {
    const path = newSessionPath({ directory: '/src/repo', remoteId: 'local', platform: 'opencode', title: 'Fix login' });
    expect(path).toBe('/session/new?dir=%2Fsrc%2Frepo&platform=opencode&title=Fix+login');
    expect(parseNewSessionParams(new URLSearchParams(path.split('?')[1]))).toEqual({
      directory: '/src/repo', remoteId: 'local', platform: 'opencode', title: 'Fix login',
    });
    expect(parseNewSessionParams(new URLSearchParams(newSessionPath({ directory: '/r', remoteId: 'box' }).split('?')[1])))
      .toEqual({ directory: '/r', remoteId: 'box', platform: undefined, title: undefined });
  });

  it('is not a new conversation without a directory', () => {
    expect(parseNewSessionParams(new URLSearchParams(''))).toBeNull();
  });
});
