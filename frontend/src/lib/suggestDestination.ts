export interface Destination { to: string; label: string }

const slug = (value: string) => value.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, '');

/** Best destination for a mistyped first path segment, matched by path or label. */
export function suggestDestination(pathname: string, destinations: Destination[]): Destination | undefined {
  const segment = slug(decodeURIComponent(pathname.split('/')[1] ?? ''));
  if (segment.length < 3) return undefined;
  const keys = (d: Destination) => [slug(d.to.split(/[/?]/)[1] ?? ''), slug(d.label)].filter(Boolean);
  return destinations.find((d) => keys(d).includes(segment))
    ?? destinations.find((d) => keys(d).some((key) => key.startsWith(segment) || segment.startsWith(key)));
}
