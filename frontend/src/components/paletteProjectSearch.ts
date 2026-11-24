import type { Project } from '../lib/api';
import { fuzzyMatch, fuzzyRank, shortPath } from '../lib/format';

function matchTier(query: string, directory: string): number {
  if (!query) return 0;
  const path = directory.toLowerCase();
  const label = shortPath(path);
  const name = path.slice(path.lastIndexOf('/') + 1);
  if (path === query) return 4;
  if (name === query || label === query) return 3;
  if (name.startsWith(query) || label.startsWith(query) || path.startsWith(query)) return 2;
  if (path.includes(query)) return 1;
  return fuzzyMatch(query, path) ? 0 : -1;
}

export function rankPaletteProjects(projects: Project[], query: string): Project[] {
  const normalizedQuery = query.trim().toLowerCase();
  // fuzzyRank preserves input order within a tier, so usage cannot outrank a better match.
  const byUsage = projects.slice().sort((a, b) => b.sessionCount - a.sessionCount || b.lastUsed - a.lastUsed);
  return fuzzyRank(byUsage, (project) => matchTier(normalizedQuery, project.directory));
}
