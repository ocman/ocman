/** Route owner-local reads through the hub, never through a remote web origin. */
export function publishedResourceURL(path: string, remoteId = 'local'): string {
  return remoteId === 'local' ? path : `/api/remote-resources/${encodeURIComponent(remoteId)}${path}`;
}

export function artifactPageURL(id: string, remoteId = 'local'): string {
  return `/artifacts/${encodeURIComponent(id)}${remoteId === 'local' ? '' : `?remoteId=${encodeURIComponent(remoteId)}`}`;
}
