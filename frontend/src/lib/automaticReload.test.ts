// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it } from 'vitest';
import { canAutomaticallyReload } from './automaticReload';
import { applyTexts, resetDraftTextsForTests, saveDraft } from './composerDraft';
import { recordFailedSend, removeFailedSend } from './failedSends';
import { useNewConversationDrafts } from './newConversationDrafts';
import { forgetDraftAttachments, pendingAttachmentWriter, rememberPendingDraftPayload, resetDraftPayloadsForTests } from './pendingDraftPayloads';

beforeEach(() => {
  resetDraftTextsForTests(); resetDraftPayloadsForTests();
  useNewConversationDrafts.setState({ drafts: [] });
});
afterEach(() => removeFailedSend('session', 'failed'));

it('allows a clean tab and empty attachment records', () => {
  rememberPendingDraftPayload('empty');
  expect(canAutomaticallyReload()).toBe(true);
});

it('protects composer text even before its IndexedDB write settles', () => {
  saveDraft('session', 'typing');
  expect(canAutomaticallyReload()).toBe(false);
});

it('protects prepared draft metadata even without text', () => {
  useNewConversationDrafts.setState({ drafts: [{ draftId: 'draft', directory: '/project' }] });
  expect(canAutomaticallyReload()).toBe(false);
});

it('also protects text hydrated from storage or another tab', () => {
  applyTexts([['stored', { text: 'saved prompt', revision: 0 }]]);
  expect(canAutomaticallyReload()).toBe(false);
});

it('protects images and pending file reads until explicitly discarded', () => {
  rememberPendingDraftPayload('image', [{ url: 'data:image/png;base64,abc', mime: 'image/png' }]);
  expect(canAutomaticallyReload()).toBe(false);
  forgetDraftAttachments('image');
  pendingAttachmentWriter('reading');
  expect(canAutomaticallyReload()).toBe(false);
  forgetDraftAttachments('reading');
  expect(canAutomaticallyReload()).toBe(true);
});

it('protects full failed-send payloads that storage may have truncated', () => {
  recordFailedSend('session', { id: 'failed', text: 'retry me', error: 'offline', failedAt: 1 });
  expect(canAutomaticallyReload()).toBe(false);
  removeFailedSend('session', 'failed');
  expect(canAutomaticallyReload()).toBe(true);
});

it('honors the existing unsaved-editor beforeunload guard without navigating', () => {
  const guard = (event: Event) => event.preventDefault();
  window.addEventListener('beforeunload', guard);
  try { expect(canAutomaticallyReload()).toBe(false); }
  finally { window.removeEventListener('beforeunload', guard); }
  expect(canAutomaticallyReload()).toBe(true);
});
