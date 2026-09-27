// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { LinkPreviewSettings } from './LinkPreviewSettings';

afterEach(() => { vi.unstubAllGlobals(); });

it('loads, adds, saves and removes custom rules', async () => {
  const fetchMock = vi.fn().mockResolvedValue({ ok: true, status: 200, json: async () => ({ rules: [] }) });
  vi.stubGlobal('fetch', fetchMock);
  render(<LinkPreviewSettings />);
  await waitFor(() => expect(screen.getByRole('button', { name: 'Add rule' })).toBeEnabled());
  expect(screen.getByRole('button', { name: 'Add rule' })).toHaveClass('oc-button');
  fireEvent.click(screen.getByRole('button', { name: 'Add rule' }));
  fireEvent.change(screen.getByRole('textbox', { name: 'Regular expression 1' }), { target: { value: 'ABC-\\d+' } });
  fireEvent.change(screen.getByRole('textbox', { name: 'Link replacement 1' }), { target: { value: 'https://tracker.example.com/issues/$&' } });
  fireEvent.click(screen.getByRole('button', { name: 'Save rules' }));
  expect(screen.getByRole('button', { name: 'Save rules' })).toHaveClass('oc-button--accent');
  await waitFor(() => expect(fetchMock).toHaveBeenCalledWith('/api/settings/link-preview-rules', expect.objectContaining({
    method: 'POST', body: JSON.stringify({ rules: [{ pattern: 'ABC-\\d+', replacement: 'https://tracker.example.com/issues/$&' }] }),
  })));
  fireEvent.click(screen.getByRole('button', { name: 'Remove rule 1' }));
  expect(screen.queryByRole('textbox', { name: 'Regular expression 1' })).toBeNull();
});
