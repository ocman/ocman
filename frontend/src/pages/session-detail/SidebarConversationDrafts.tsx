import { useLayoutEffect, useRef, useState } from 'react';
import { flushSync } from 'react-dom';
import { useLocation, useNavigate } from 'react-router-dom';
import { Button } from '../../components/Control';
import { ArchiveButton } from '../../components/ArchiveButton';
import { StatusBadge } from '../../components/StatusBadge';
import { InlineAlert } from '../../components/InlineAlert';
import { shortPath, fuzzyMatch, relativeTime } from '../../lib/format';
import { newSessionPath } from '../../lib/newSessionPath';
import { forgetConversationDraft, useNewConversationDrafts, type ConversationDraft } from '../../lib/newConversationDrafts';
import './SidebarConversationDrafts.css';

export function SidebarConversationDrafts({ searchQuery, drafts: supplied, inGroup = false }: { searchQuery: string; drafts?: ConversationDraft[]; inGroup?: boolean }) {
  const stored = useNewConversationDrafts((state) => state.drafts);
  const drafts = supplied ?? stored;
  return drafts.length ? <DraftRows drafts={drafts} searchQuery={supplied ? '' : searchQuery} inGroup={inGroup} /> : null;
}

function DraftRows({ drafts, searchQuery, inGroup }: { drafts: ConversationDraft[]; searchQuery: string; inGroup: boolean }) {
  const navigate = useNavigate();
  const location = useLocation();
  const currentLocation = useRef<typeof location | undefined>(location);
  useLayoutEffect(() => { currentLocation.current = location; return () => { currentLocation.current = undefined; }; }, [location]);
  const [failed, setFailed] = useState<{ draftId: string; message: string }>();
  const [discarding, setDiscarding] = useState<string>();
  const discard = async (draft: ConversationDraft) => {
    setFailed(undefined);
    setDiscarding(draft.draftId);
    try {
      // Only a stored discard moves on; a failure keeps the draft, its text and files.
      await forgetConversationDraft(draft.draftId, () => {
        if (currentLocation.current?.key !== location.key || draft.draftId !== activeId) return;
        const next = useNewConversationDrafts.getState().drafts.find((entry) => entry.draftId !== draft.draftId);
        flushSync(() => navigate(next ? newSessionPath(next) : '/', { replace: true }));
      });
    } catch (error) {
      setFailed({ draftId: draft.draftId, message: `Could not discard the draft: ${error instanceof Error ? error.message : String(error)}` });
    } finally { setDiscarding(undefined); }
  };
  const activeId = location.pathname === '/session/new' ? new URLSearchParams(location.search).get('draftId') || 'new' : null;
  const visible = drafts.filter((draft) => draft.draftId === activeId || !searchQuery.trim() ||
    fuzzyMatch(searchQuery.trim(), `${draft.title || ''} ${draft.directory} ${draft.remoteId || 'local'}`));
  if (!visible.length) return null;
  return <>
    {visible.map((draft) => <div key={draft.draftId} data-testid="conversation-draft" aria-selected={draft.draftId === activeId}
      className={`session-sidebar-item session-sidebar-draft ${inGroup ? 'in-group' : 'flat'}${draft.draftId === activeId ? ' active' : ''}`}>
      {inGroup && <StatusBadge status="done" compact draft seen />}
      <Button variant="ghost" className="session-sidebar-item-body session-sidebar-draft-open" aria-current={draft.draftId === activeId ? 'page' : undefined}
        onClick={() => navigate(newSessionPath(draft))}>
        {!inGroup && <span className="session-sidebar-project"><StatusBadge status="done" compact draft seen />
          <span className="session-sidebar-project-path">{shortPath(draft.directory)}{draft.remoteId && draft.remoteId !== 'local' ? ` · ${draft.remoteId}` : ''}</span>
          {draft.createdAt && <span className="session-sidebar-time">{relativeTime(draft.createdAt).replace('just now', 'now').replace(' ago', '')}</span>}
        </span>}
        <span className="session-sidebar-title">{draft.title || `New session ${useNewConversationDrafts.getState().drafts.findIndex((entry) => entry.draftId === draft.draftId) + 1}`}</span>
        {!inGroup && <span className="session-sidebar-git-slot" />}
      </Button>
      <span className="session-sidebar-meta">
        {inGroup && draft.createdAt && <span className="session-sidebar-time">{relativeTime(draft.createdAt).replace('just now', 'now').replace(' ago', '')}</span>}
        <span className="session-sidebar-actions">
        <ArchiveButton className="session-sidebar-archive-btn" label="Discard draft" disabled={discarding === draft.draftId} onClick={() => void discard(draft)} />
      </span></span>
      {failed?.draftId === draft.draftId && <InlineAlert compact retrying={discarding === draft.draftId} onRetry={() => void discard(draft)}>{failed.message}</InlineAlert>}
    </div>)}
  </>;
}
