import type { GitInfo, Session } from '../../lib/api';
import { cleanTitle, fuzzyMatch } from '../../lib/format';

export function matchesSessionSearch(query: string, session: Session, gitInfo?: GitInfo): boolean {
  return [cleanTitle(session.title), session.directory, gitInfo?.branch ?? session.branch]
    .some((value) => !!value && fuzzyMatch(query, value));
}
