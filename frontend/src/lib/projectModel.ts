// Remembers the last model a user picked, scoped per project directory.
// A brand-new session (no turns yet) seeds its composer from this so it
// inherits the project's most-recent choice instead of the backend default.
// Mirrors composerDraft.ts (single localStorage key, keyed map).
const PROJECT_MODELS_KEY = 'ocman.projectModels.v1';

type ProjectModels = Record<string, string>;

function load(): ProjectModels {
  if (typeof window === 'undefined') return {};
  try {
    const raw = window.localStorage.getItem(PROJECT_MODELS_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as ProjectModels;
    if (!parsed || typeof parsed !== 'object') return {};
    return parsed;
  } catch {
    return {};
  }
}

function save(data: ProjectModels) {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(PROJECT_MODELS_KEY, JSON.stringify(data));
  } catch {
    // Ignore storage errors (private mode, quotas, etc.)
  }
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
