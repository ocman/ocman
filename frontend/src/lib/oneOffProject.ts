export const ONE_OFF_PROJECT = 'one-off';

// Matches the workspace names allocated by the owner, including legacy sessions.
export function isOneOffDirectory(directory: string): boolean {
  return /(?:^|\/)ocman-one-off-[^/]+\/?$/.test(directory);
}

export function sessionProjectDirectory(directory: string): string {
  return isOneOffDirectory(directory) ? ONE_OFF_PROJECT : directory;
}
