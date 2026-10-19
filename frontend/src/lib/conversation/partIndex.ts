// Memoized id -> index maps for the view's message and part arrays. A map is
// built once per array identity; replacing one element carries it over, so
// upserts and deltas cost a lookup plus one array copy.

const indexes = new WeakMap<readonly { id: string }[], Map<string, number>>();

function indexFor(list: readonly { id: string }[]): Map<string, number> {
  let index = indexes.get(list);
  if (!index) {
    index = new Map();
    for (let i = 0; i < list.length; i++) index.set(list[i].id, i);
    indexes.set(list, index);
  }
  return index;
}

export function findIndexById(list: readonly { id: string }[], id: string): number {
  return indexFor(list).get(id) ?? -1;
}

/** Copy `list` with `item` at `i`, keeping the index (ids are unchanged). */
export function replaceAt<T extends { id: string }>(list: readonly T[], i: number, item: T): T[] {
  const next = [...list];
  next[i] = item;
  const index = indexes.get(list);
  if (index) indexes.set(next, index);
  return next;
}
