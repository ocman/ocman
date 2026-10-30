/** Classify without fetching. Only local/embedded sources may enter the native
 * image gallery; remote HTTP(S) sources require the sandboxed consent path. */
export function imageSource(src: string): 'local' | 'blocked' | URL {
  if (!src) return 'local';
  try {
    const url = new URL(src, window.location.href);
    if (url.username || url.password) return 'blocked';
    if (url.protocol === 'data:') return /^data:image\//i.test(src) ? 'local' : 'blocked';
    if (url.protocol === 'blob:') return url.origin === window.location.origin ? 'local' : 'blocked';
    if (url.protocol !== 'http:' && url.protocol !== 'https:') return 'blocked';
    return url.origin === window.location.origin ? 'local' : url;
  } catch { return 'blocked'; }
}
