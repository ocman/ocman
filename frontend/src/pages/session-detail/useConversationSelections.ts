import { useCallback, useState } from 'react';
import type { ConversationDraft } from '../../lib/newConversationDrafts';
import { NEW_SESSION_ID, newSessionPath } from '../../lib/newSessionPath';
import type { SessionTarget } from '../../components/assistant/ComposerSelectorRow';
import type { NewConversationProps } from './NewConversation';

/** Explicit connection switches differ from routes updated by a model pick. */
export function useConversationSelections(params: NewConversationProps['params'], saved: ConversationDraft | undefined, navigate: NewConversationProps['navigate']) {
  const [selectedModel, setSelectedModel] = useState(saved?.model || '');
  const [selectedAgent, setSelectedAgent] = useState(saved?.agent || '');
  const [selectedReasoning, setSelectedReasoning] = useState(saved?.reasoning || '');
  const [targetPick, setTarget] = useState<SessionTarget | undefined>(saved?.target as SessionTarget | undefined);
  const [permissionMode, setPermissionMode] = useState(saved?.permissionMode || 'default');
  // Model-driven route changes retain the pick; machine changes cannot reuse it.
  const machineKey = JSON.stringify([params.remoteId || 'local', params.directory]);
  const [selectionKey, setSelectionKey] = useState(machineKey);
  if (selectionKey !== machineKey) {
    setSelectionKey(machineKey);
    setSelectedModel(''); setSelectedAgent(''); setSelectedReasoning('');
    setTarget(saved?.target as SessionTarget | undefined);
  }
  const selectConnection = useCallback((id: string, compatible: boolean) => {
    if (!compatible) {
      setSelectedModel(''); setSelectedAgent(''); setSelectedReasoning('');
    }
    navigate(newSessionPath({ ...params, draftId: params.draftId || NEW_SESSION_ID, platform: id }));
  }, [params, navigate]);
  return { selectedModel, setSelectedModel, selectedAgent, setSelectedAgent, selectedReasoning, setSelectedReasoning,
    targetPick, setTarget, permissionMode, setPermissionMode, selectConnection };
}
