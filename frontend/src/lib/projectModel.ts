// Remembers the last model a user picked, scoped per project directory.
// A brand-new session (no turns yet) seeds its composer from this so it
// inherits the project's most-recent choice instead of the backend default.
// Mirrors composerDraft.ts (single localStorage key, keyed map).
import { readStored, writeStorage } from './storage';

const PROJECT_MODELS_KEY = 'ocman.projectModels.v1';

type ProjectModels = Record<string, string>;

function load(): ProjectModels {
  return readStored<ProjectModels>(PROJECT_MODELS_KEY, {}, (raw) => {
    const parsed = JSON.parse(raw) as ProjectModels;
    return parsed && typeof parsed === 'object' ? parsed : {};
  });
}

function save(data: ProjectModels) {
  writeStorage(PROJECT_MODELS_KEY, JSON.stringify(data));
}

export function getProjectModel(directory: string): string {
  if (!directory) return '';
  return load()[directory] || '';
}

export function saveProjectModel(directory: string, model: string) {
  if (!directory || !model) return;
  const data = load();
  data[directory] = model;
  save(data);
}
