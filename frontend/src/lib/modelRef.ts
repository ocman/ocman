// Model references are "[<platform>:]<provider>/<model>" (Go:
// platforms.SplitModel). No prefix means OpenCode, so stored references keep
// their meaning. Only a colon before the first slash is a prefix: model ids
// may contain colons (openrouter/meta-llama/llama-3:free).

const OPENCODE = 'opencode';

export function splitModelRef(ref: string): { platform: string; model: string } {
  const slash = ref.indexOf('/');
  const colon = slash < 0 ? -1 : ref.slice(0, slash).indexOf(':');
  return colon > 0 ? { platform: ref.slice(0, colon), model: ref.slice(colon + 1) } : { platform: '', model: ref };
}

/** Prefixes a plain provider/model with its base platform; OpenCode stays bare. */
export function qualifyModelRef(platform: string | undefined, model: string): string {
  return !model || !platform || platform === OPENCODE ? model : `${platform}:${model}`;
}

/** The base platform a reference starts a new session on ('' for none). */
export function modelRefPlatform(ref: string): string {
  return ref ? splitModelRef(ref).platform || OPENCODE : '';
}

/** The owner-local platform of an id: "r-box:claudecode" is "claudecode". */
export function basePlatform(id: string): string {
  return id.slice(id.lastIndexOf(':') + 1);
}

/** A reference usable on a session of platformId: its own prefix is
 *  stripped, another platform's model is dropped. */
export function modelRefForSession(ref: string, platformId: string | undefined): string {
  const { platform, model } = splitModelRef(ref);
  if (!platform) return ref;
  return platformId && platform === basePlatform(platformId) ? model : '';
}
