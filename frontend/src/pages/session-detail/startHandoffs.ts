import type { StartSteps } from './StartProgress';

// A finished start, keyed by the new session id, so the session view keeps
// showing it instead of an empty thread until the first message arrives.
// ponytail: never pruned; one tiny entry per conversation started this page load.
export const startHandoffs = new Map<string, { prompt: string; steps: StartSteps }>();
