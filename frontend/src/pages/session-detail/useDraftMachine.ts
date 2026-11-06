import { useCallback, useEffect, useRef } from 'react';
import type { TargetCandidate } from '../../lib/api.types';
import { shortPath } from '../../lib/format';
import { useHeaderInfo } from '../../lib/headerContext';
import { newSessionPath, NEW_SESSION_ID, type NewSessionParams } from '../../lib/newSessionPath';
import { getConversationDraft, rememberConversationDraft, useNewConversationDrafts, type ConversationDraft } from '../../lib/newConversationDrafts';
import { projectRootForDirectory } from '../../lib/worktrees';

export function useDraftMachine(params: NewSessionParams, selections: Pick<ConversationDraft, 'model' | 'agent' | 'reasoning' | 'target'>,
  locked: () => boolean, navigate: (path: string) => void) {
  const { directory, platform, title } = params;
  const remoteId = params.remoteId || 'local';
  const draftId = params.draftId || NEW_SESSION_ID;
  const machineEdit = useRef('');
  const clone = useNewConversationDrafts((state) => {
    const draft = state.drafts.find((d) => d.draftId === draftId);
    return draft?.directory === directory && draft.remoteId === remoteId ? draft.clone : undefined;
  });
  const { model, agent, reasoning, target } = selections;
  const saveDraft = useCallback(() => {
    const saved = getConversationDraft(draftId);
    const sameLocation = saved?.directory === directory && saved.remoteId === remoteId;
    rememberConversationDraft({ directory, remoteId, platform, title, draftId, model, agent, reasoning, target,
      clone: sameLocation ? saved.clone : undefined });
  }, [directory, remoteId, platform, title, draftId, model, agent, reasoning, target]);
  useEffect(saveDraft, [saveDraft]);
  const saveSubmittedRoute = useCallback(() => {
    // Only undo this view's unchanged machine edit. A peer's newer edits must
    // remain available for completion to move into a replacement draft.
    if (machineEdit.current && machineEdit.current === JSON.stringify(getConversationDraft(draftId))) saveDraft();
  }, [draftId, saveDraft]);
  const { setInfo } = useHeaderInfo();
  useEffect(() => {
    setInfo({ sessionId: NEW_SESSION_ID, sessionProject: shortPath(projectRootForDirectory(directory)), sessionProjectFull: directory, sessionRemoteId: remoteId });
    return () => setInfo({});
  }, [directory, remoteId, setInfo]);

  const onMachineChange = async (machine: TargetCandidate) => {
    const start = useNewConversationDrafts.getState().starts[draftId];
    if (locked() || start && !start.error && !start.sessionId) throw new Error('Session creation is already in progress');
    const next = { directory: machine.dir, remoteId: machine.remoteId, platform: machine.platform, title, draftId };
    rememberConversationDraft({ ...next, target: machine.cloneUrl || target?.startsWith('dir:') ? 'current' : target, model: '', agent: '', reasoning: '',
      clone: machine.cloneUrl ? { url: machine.cloneUrl, source: clone?.source || { directory, remoteId } } : undefined });
    machineEdit.current = JSON.stringify(getConversationDraft(draftId));
    navigate(newSessionPath(next));
  };
  return { cloneURL: clone?.url, machineSource: clone?.source, onMachineChange, saveDraft: saveSubmittedRoute };
}
