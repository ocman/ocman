import { api, type StartSessionRequest } from '../../lib/api';
import type { AttachedFileRef } from '../../components/assistant/useComposerAttachments';

// Successful uploads survive a failed upload/send retry. The closure owns
// browser Files until delivery, so a route change cannot drop them.
export function sendFirstFiles(send: NonNullable<StartSessionRequest['send']>, files: File[]) {
  const uploaded: AttachedFileRef[] = [];
  return async (sessionId: string, platform: string) => {
    for (let i = uploaded.length; i < files.length; i++) {
      const file = files[i];
      const saved = await api.uploadComposerAttachment(sessionId, file);
      uploaded.push({ path: saved.path, name: saved.name || file.name, mime: saved.mime || file.type || 'application/octet-stream' });
    }
    const references = `Attached files saved on disk:\n${uploaded.map((file) => `- ${file.path} (${file.mime})`).join('\n')}`;
    await api.sendMessage(sessionId, [send.message, references].filter(Boolean).join('\n\n'), send.images,
      send.model, send.agent, send.reasoning, platform);
  };
}
