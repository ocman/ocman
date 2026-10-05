/** A random id; `crypto.randomUUID` exists only in secure contexts (plain-HTTP proxies lack it). */
export function randomId(): string {
  return crypto.randomUUID?.() ?? Array.from(crypto.getRandomValues(new Uint32Array(4)), (value) => value.toString(16).padStart(8, '0')).join('');
}
