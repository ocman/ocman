import { expect, it } from 'vitest';
import { isOneOffDirectory, sessionProjectDirectory } from './oneOffProject';

it.each(['/tmp/ocman-one-off-123', '/private/var/folders/a/T/ocman-one-off-456/'])('recognizes workspace %s', (path) => {
  expect(isOneOffDirectory(path)).toBe(true);
  expect(sessionProjectDirectory(path)).toBe('one-off');
});

it.each(['', '/repo/one-off', '/repo/ocman-one-off-', '/tmp/ocman-one-off-123/nested'])('preserves ordinary directory %s', (path) => {
  expect(isOneOffDirectory(path)).toBe(false);
  expect(sessionProjectDirectory(path)).toBe(path);
});
