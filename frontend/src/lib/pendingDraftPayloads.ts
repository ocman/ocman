import type { AttachedImage, AttachedFileRef } from '../components/assistant/useComposerAttachments';

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
