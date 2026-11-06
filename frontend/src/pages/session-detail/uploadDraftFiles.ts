import { apiFetch, raiseForUnauthorized, readJSON } from '../../lib/api.requests';

export async function uploadDraftFiles(id: string, text: string, files: File[]) {
  const saved = [];
  for (const file of files) {
    const form = new FormData();
    form.append('file', file);
    const response = await apiFetch(`/api/session-drafts/${encodeURIComponent(id)}/attachment`, { method: 'POST', body: form });
    if (!response.ok) {
      await raiseForUnauthorized(response);
      throw new Error((await response.text()).trim() || `HTTP ${response.status}`);
    }
    const uploaded = await readJSON<{ path: string; mime: string }>(response);
    saved.push(`- ${uploaded.path} (${uploaded.mime})`);
  }
  return [text, `Attached files saved on disk:\n${saved.join('\n')}`].filter(Boolean).join('\n\n');
}
