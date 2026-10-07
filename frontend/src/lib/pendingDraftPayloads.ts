import type { AttachedImage, AttachedFileRef } from '../components/assistant/useComposerAttachments';
import { getDraftVersion } from './composerDraft';

const payloads = new Map<string, { images: AttachedImage[]; files: AttachedFileRef[] }>();

export function rememberPendingDraftPayload(draftId: string, images: AttachedImage[] = [], files: File[] = []) {
  payloads.set(draftId, { images,
    files: files.map((file) => ({ file, path: '', name: file.name, mime: file.type || 'application/octet-stream' })) });
}

export function getPendingDraftPayload(draftId: string) {
  return payloads.get(draftId);
}

export const updateDraftAttachments = (draftId: string, payload: { images: AttachedImage[]; files: AttachedFileRef[] }) => payloads.set(draftId, payload);
export const forgetDraftAttachments = (draftId: string) => payloads.delete(draftId);

export function transferDraftAttachments(from: string, to: string) {
  const payload = payloads.get(from);
  if (payload) payloads.set(to, payload);
}

/** Capture the initiating identity before FileReader yields, merge accepted batches later. */
export function pendingAttachmentWriter(draftId: string) {
  const version = getDraftVersion(draftId);
  return (batch: { images: AttachedImage[]; files: AttachedFileRef[] }) => {
    if (getDraftVersion(draftId) !== version) return;
    const current = payloads.get(draftId) || { images: [], files: [] };
    payloads.set(draftId, { images: [...current.images, ...batch.images], files: [...current.files, ...batch.files] });
  };
}
