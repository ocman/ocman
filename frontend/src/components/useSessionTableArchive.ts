import { useRef, useState, type MouseEvent } from 'react';
import type { Session } from '../lib/api';
import { useApiStore } from '../lib/apiStore';
import { remoteLog } from '../lib/remoteLog';

/** Shared mutation state lets both table layouts hide pending phone archives. */
export function useSessionTableArchive() {
  const archiveSession = useApiStore(state => state.archiveSession);
  const inFlight = useRef(new Set<string>());
  const [archivingSessionIds, setArchivingSessionIds] = useState<Set<string>>(new Set());
  const [locallyArchivedSessionIds, setLocallyArchivedSessionIds] = useState<Set<string>>(new Set());
  const [archiveError, setArchiveError] = useState<string | null>(null);

  async function handleArchiveSession(event: MouseEvent, session: Session) {
    event.stopPropagation();
    if (inFlight.current.has(session.id)) return;
    inFlight.current.add(session.id);
    setArchivingSessionIds(previous => new Set(previous).add(session.id));
    setArchiveError(null);
    try {
      await archiveSession(session.platform, session.id, session.timeUpdated, true);
      setLocallyArchivedSessionIds(previous => new Set(previous).add(session.id));
    } catch (error) {
      remoteLog.error('Failed to archive session', error);
      setArchiveError('Could not archive session. Please try again.');
    } finally {
      inFlight.current.delete(session.id);
      setArchivingSessionIds(previous => {
        const next = new Set(previous);
        next.delete(session.id);
        return next;
      });
    }
  }
  return { archivingSessionIds, locallyArchivedSessionIds, archiveError, handleArchiveSession };
}
