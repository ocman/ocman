import { beforeEach, expect, it, vi } from 'vitest';
import { uploadDraftFiles } from './uploadDraftFiles';
import { apiFetch, raiseForUnauthorized, readJSON } from '../../lib/api.requests';

vi.mock('../../lib/api.requests', () => ({ apiFetch: vi.fn(), raiseForUnauthorized: vi.fn(), readJSON: vi.fn() }));
beforeEach(() => vi.clearAllMocks());

it('persists files on the draft owner and includes all returned paths in the prompt', async () => {
  vi.mocked(apiFetch).mockResolvedValue(new Response('{}'));
  vi.mocked(readJSON).mockResolvedValueOnce({ path: '/owner/a.txt', mime: 'text/plain' }).mockResolvedValueOnce({ path: '/owner/b.txt', mime: 'text/plain' });
  const result = await uploadDraftFiles('ocm_id', 'Read these', [new File(['a'], 'a.txt'), new File(['b'], 'b.txt')]);
  expect(result).toBe('Read these\n\nAttached files saved on disk:\n- /owner/a.txt (text/plain)\n- /owner/b.txt (text/plain)');
  expect(apiFetch).toHaveBeenCalledTimes(2);
  expect(apiFetch).toHaveBeenCalledWith('/api/session-drafts/ocm_id/attachment', expect.objectContaining({ method: 'POST' }));
});

it.each(['disk full', ''])('leaves upload failures visible: %s', async (body) => {
  const response = new Response(body, { status: 500 });
  vi.mocked(apiFetch).mockResolvedValue(response);
  await expect(uploadDraftFiles('id', '', [new File(['a'], 'a.txt')])).rejects.toThrow(body || 'HTTP 500');
  expect(raiseForUnauthorized).toHaveBeenCalledWith(response);
  expect(readJSON).not.toHaveBeenCalled();
});
