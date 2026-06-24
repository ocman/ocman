import type { Project } from '../lib/api';

export type ProjectPaletteResult =
  | { kind: 'project'; project: Project }
  | { kind: 'directory'; directory: string };

export function absoluteDirectoryQuery(query: string): string | null {
  const directory = query.trim();
  if (!directory.startsWith('/')) return null;
  return directory;
}

export function buildProjectPaletteResults(projectList: Project[], query: string): ProjectPaletteResult[] {
  const directory = absoluteDirectoryQuery(query);
  if (!query.trim()) {
    return projectList
      .slice()
      .sort((a, b) => b.lastUsed - a.lastUsed)
      .slice(0, 20)
      .map((p) => ({ kind: 'project' as const, project: p }));
  }

  const q = query.toLowerCase();
  const projectResults = projectList
    .filter((p) => p.directory.toLowerCase().includes(q))
    .sort((a, b) => b.lastUsed - a.lastUsed)
    .slice(0, 20)
    .map((p) => ({ kind: 'project' as const, project: p }));

  if (!directory || projectList.some((p) => p.directory === directory)) {
    return projectResults;
  }

  return [...projectResults, { kind: 'directory', directory }];
}
