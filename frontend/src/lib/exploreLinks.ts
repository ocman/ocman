import { createContext } from 'react';

export const ExploreLinksContext = createContext(false);
export const EXPLORE_FILE_PARAM = 'exploreFile';
const EXPLORE_FILE_PREFIX = 'ocman://explore/file/';

export function isExploreFilePath(path: string): boolean {
  return !/[\\\p{Cc}]/u.test(path) && !/^[a-z]:/i.test(path) &&
    path.split('/').every(part => part && part !== '.' && part !== '..');
}

export function fileLinkPath(href: string): string | undefined {
  // Decode each segment without allowing escaped separators or normalizing dot segments.
  if (!href.startsWith(EXPLORE_FILE_PREFIX)) return;
  const raw = href.slice(EXPLORE_FILE_PREFIX.length);
  if (/[?#]|%2f/i.test(raw)) return;
  try {
    const path = decodeURIComponent(raw);
    if (isExploreFilePath(path)) return path;
  } catch {
    // Malformed percent escapes are not file links.
  }
}
