// @vitest-environment jsdom
import { expect, it } from 'vitest';
import { saveDraft } from './composerDraft';
import { forgetDraftAttachments, getPendingDraftPayload, pendingAttachmentWriter, transferDraftAttachments } from './pendingDraftPayloads';

it('finishes accepted attachments despite empty-text autosave, and releases their processing count', () => {
  const finish = pendingAttachmentWriter('empty-autosave');
  saveDraft('empty-autosave', '');
  finish({ images: [{ url: 'image', mime: 'image/png' }], files: [] });
  expect(getPendingDraftPayload('empty-autosave')).toMatchObject({ pending: 0, images: [{ url: 'image', mime: 'image/png' }] });
});

it('does not restore an explicitly discarded attachment batch', () => {
  const finish = pendingAttachmentWriter('discarded-batch');
  forgetDraftAttachments('discarded-batch');
  finish({ images: [{ url: 'image', mime: 'image/png' }], files: [] });
  expect(getPendingDraftPayload('discarded-batch')).toBeUndefined();
});

it('finishes an accepted batch on its relocated owner without stranding the replacement lock', () => {
  const finish = pendingAttachmentWriter('relocating-batch');
  transferDraftAttachments('relocating-batch', 'replacement-batch');
  forgetDraftAttachments('relocating-batch');
  finish({ images: [{ url: 'image', mime: 'image/png' }], files: [] });
  expect(getPendingDraftPayload('replacement-batch')).toMatchObject({ pending: 0, images: [{ url: 'image', mime: 'image/png' }] });
  expect(getPendingDraftPayload('relocating-batch')).toBeUndefined();
});
