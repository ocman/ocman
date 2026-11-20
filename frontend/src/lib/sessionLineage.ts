import type { Session } from './api';

export const lineageKey = ({ platform, id }: Pick<Session, 'platform' | 'id'>) => `${platform}\0${id}`;

function parentKey(session: Session) {
  if (session.parentId) return lineageKey({ id: session.parentId, platform: session.platform });
  if (session.creator) return lineageKey({ id: session.creator.sessionId, platform: session.creator.platform });
}

/** Native ancestry takes precedence, as it does in the session list. */
export function sessionLineage(current: Session, nativeTree: readonly Session[], catalog: readonly Session[]) {
  const ownKey = lineageKey(current);
  const sessions = new Map(catalog.map(s => [lineageKey(s), s]));
  for (const session of [...nativeTree, current]) {
    const key = lineageKey(session);
    sessions.set(key, { ...sessions.get(key), ...session });
  }

  const children = new Map<string, Session[]>();
  for (const [key, session] of sessions) {
    const parent = parentKey(session);
    if (!parent || parent === key) continue;
    const siblings = children.get(parent) ?? [];
    siblings.push(session);
    children.set(parent, siblings);
  }
  for (const siblings of children.values()) siblings.sort((a, b) => a.timeCreated - b.timeCreated);

  let root = current;
  let unavailableParent = false;
  const path = new Set([ownKey]);
  for (;;) {
    const parent = parentKey(sessions.get(lineageKey(root)) ?? root);
    if (!parent || path.has(parent)) break;
    const next = sessions.get(parent);
    if (!next) {
      unavailableParent = true;
      break;
    }
    root = next;
    path.add(parent);
  }

  // A malformed cycle can only enter this family at the chosen root.
  const rootKey = lineageKey(root);
  const rootParent = parentKey(root);
  if (rootParent && rootParent !== rootKey && path.has(rootParent)) {
    const remaining = (children.get(rootParent) ?? []).filter(s => lineageKey(s) !== rootKey);
    if (remaining.length) children.set(rootParent, remaining);
    else children.delete(rootParent);
  }
  return { root, children, path, unavailableParent };
}
