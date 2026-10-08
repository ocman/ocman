import { useState } from 'react';
import { flushSync } from 'react-dom';
import { useLocation, useNavigate } from 'react-router-dom';
import { Button } from '../../components/Control';
import { IconButton } from '../../components/IconButton';
import { InlineAlert } from '../../components/InlineAlert';
import { shortPath, fuzzyMatch } from '../../lib/format';
import { newSessionPath } from '../../lib/newSessionPath';
import { forgetConversationDraft, useNewConversationDrafts, type ConversationDraft } from '../../lib/newConversationDrafts';
import './SidebarConversationDrafts.css';

export function SidebarConversationDrafts({ searchQuery }: { searchQuery: string }) {
  const drafts = useNewConversationDrafts((state) => state.drafts);
  return drafts.length ? <DraftRows drafts={drafts} searchQuery={searchQuery} /> : null;
}

function DraftRows({ drafts, searchQuery }: { drafts: ConversationDraft[]; searchQuery: string }) {
  const navigate = useNavigate();
  const location = useLocation();
  const [failed, setFailed] = useState<{ draftId: string; message: string }>();
  const [discarding, setDiscarding] = useState<string>();
  const discard = async (draft: ConversationDraft) => {
    setFailed(undefined);
    setDiscarding(draft.draftId);
    try {
      // Only a stored discard moves on; a failure keeps the draft, its text and files.
      await forgetConversationDraft(draft.draftId, () => {
        const next = drafts.find((entry) => entry.draftId !== draft.draftId);
        if (draft.draftId === activeId) flushSync(() => navigate(next ? newSessionPath(next) : '/', { replace: true }));
      });
    } catch (error) {
      setFailed({ draftId: draft.draftId, message: `Could not discard the draft: ${error instanceof Error ? error.message : String(error)}` });
    } finally { setDiscarding(undefined); }
  };
  const activeId = location.pathname === '/session/new' ? new URLSearchParams(location.search).get('draftId') || 'new' : null;
  const visible = drafts.filter((draft) => draft.draftId === activeId || !searchQuery.trim() ||
    fuzzyMatch(searchQuery.trim(), `${draft.title || ''} ${draft.directory} ${draft.remoteId || 'local'}`));
  if (!visible.length) return null;
  return <div className="session-sidebar-group" aria-label="Prepared sessions">
    <div className="session-sidebar-group-header-row">
      <div className="session-sidebar-group-header"><span className="session-sidebar-group-label">Drafts</span></div>
    </div>
    {visible.map((draft) => <div key={draft.draftId} className={`session-sidebar-item flat${draft.draftId === activeId ? ' active' : ''}`}>
      <Button variant="ghost" className="session-sidebar-item-body session-sidebar-draft-open" aria-current={draft.draftId === activeId ? 'page' : undefined}
        onClick={() => navigate(newSessionPath(draft))}>
        <span className="session-sidebar-title">{draft.title || `New session ${drafts.indexOf(draft) + 1}`}</span>
        <span className="session-sidebar-project">{shortPath(draft.directory)}{draft.remoteId && draft.remoteId !== 'local' ? ` · ${draft.remoteId}` : ''}</span>
      </Button>
      <IconButton label="Discard draft" icon="bi-x-lg" disabled={discarding === draft.draftId} onClick={() => void discard(draft)} />
      {failed?.draftId === draft.draftId && <InlineAlert compact retrying={discarding === draft.draftId} onRetry={() => void discard(draft)}>{failed.message}</InlineAlert>}
    </div>)}
  </div>;
}
