import { hasComposerDrafts } from './composerDraft';
import { hasLiveFailedSends } from './failedSends';
import { useNewConversationDrafts } from './newConversationDrafts';
import { hasPendingDraftPayloads } from './pendingDraftPayloads';

/** Automatic refresh must never discard a live draft, even if its save failed. */
export function canAutomaticallyReload() {
  if (hasComposerDrafts() ||
      useNewConversationDrafts.getState().drafts.length || hasPendingDraftPayloads() || hasLiveFailedSends()) return false;
  // Honor the same unsaved-editor guards as real navigation, without asking the
  // browser to display a dialog or relying on sticky user activation.
  return window.dispatchEvent(new Event('beforeunload', { cancelable: true }));
}
