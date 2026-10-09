// @vitest-environment jsdom
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { api, type FactoryIssue } from '../../lib/api';
import { MarkdownContent } from './MarkdownText';

const issue: FactoryIssue = { id: 'upload-fix.1.2', epicId: 'upload-fix', project: '/repo', kind: 'implementation', title: 'Fix attachment uploads', status: 'open' };

function markdown(text: string, factoryCards = true) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}><MemoryRouter><MarkdownContent text={text} factoryCards={factoryCards} /></MemoryRouter></QueryClientProvider>);
}

afterEach(() => vi.restoreAllMocks());

describe('Markdown issue references', () => {
  it.each([true, false])('adds references in every prose structure, factoryCards=%s', (factoryCards) => {
    const fetch = vi.spyOn(api, 'factoryIssues').mockResolvedValue([issue]);
    markdown('# upload-fix.1.2\n\n**#upload-fix.1.2** and `upload-fix.1.2`.\n\n- upload-fix.1.2\n\n> upload-fix.1.2\n\n| Issue |\n| --- |\n| upload-fix.1.2 |', factoryCards);
    expect(screen.getAllByRole('button', { name: /upload-fix\.1\.2/ })).toHaveLength(6);
    expect(fetch).not.toHaveBeenCalled();
    expect(screen.getAllByRole('button', { name: /upload-fix\.1\.2/ })[2].querySelector('code')).toHaveTextContent(issue.id);
  });

  it('does not reinterpret versions, URLs, ordinary numbers, links, or code snippets', () => {
    const { container } = markdown('v1.2.3, #123, 1.2, /upload-fix.1.2/file, upload-fix.1.2abc\n\n[upload-fix.1.2](https://example.com)\n\nhttps://example.com/upload-fix.1.2\n\n`run upload-fix.1.2`\n\n```text\nupload-fix.1.2\n```');
    expect(screen.queryByRole('button', { name: /upload-fix\.1\.2/ })).not.toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'upload-fix.1.2' })).toHaveAttribute('href', 'https://example.com');
    expect(container.querySelector('pre code')).toHaveTextContent(issue.id);
  });

  it('loads only the referenced epic on hover, keeps the card reachable, and opens the issue', async () => {
    const fetch = vi.spyOn(api, 'factoryIssues').mockResolvedValue([issue]);
    markdown('See upload-fix.1.2.');
    const trigger = screen.getByRole('button', { name: issue.id });
    expect(trigger).toHaveClass('oc-factory-issue-reference');
    fireEvent.mouseEnter(trigger);
    const card = screen.getByRole('dialog', { name: `Issue ${issue.id}` });
    expect(card).not.toHaveFocus();
    expect(await within(card).findByText(issue.title)).toBeVisible();
    expect(fetch).toHaveBeenCalledWith('upload-fix', expect.any(AbortSignal));
    fireEvent.mouseLeave(trigger);
    fireEvent.mouseEnter(within(card).getByText(issue.title).parentElement!);
    await new Promise((resolve) => setTimeout(resolve, 180));
    expect(card).toBeVisible();
    const open = within(card).getByRole('link', { name: 'Open issue' });
    expect(open).toHaveAttribute('href', `/factory/issues/${issue.id}`);
    await userEvent.click(open);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('supports tapping, keyboard focus, Escape, and outside dismissal without reopening', async () => {
    vi.spyOn(api, 'factoryIssues').mockResolvedValue([issue]);
    markdown('See upload-fix.1.2.');
    const trigger = screen.getByRole('button', { name: issue.id });
    await userEvent.click(trigger);
    const card = screen.getByRole('dialog');
    await within(card).findByText(issue.title);
    await userEvent.tab();
    expect(within(card).getByRole('link', { name: 'Open issue' })).toHaveFocus();
    await userEvent.keyboard('{Escape}');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(trigger).toHaveFocus();
    await userEvent.click(trigger);
    expect(screen.getByRole('dialog')).toBeVisible();
    await userEvent.click(document.body);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    act(() => trigger.blur());
    act(() => trigger.focus());
    expect(screen.getByRole('dialog')).toBeVisible();
  });

  it('previews explicit issue links and shares cached titles across references', async () => {
    const fetch = vi.spyOn(api, 'factoryIssues').mockResolvedValue([issue]);
    markdown('[Upload task](/factory/issues/upload-fix.1.2) and upload-fix.1.2');
    await userEvent.click(screen.getByRole('button', { name: 'Upload task' }));
    await screen.findByText(issue.title);
    await userEvent.keyboard('{Escape}');
    await userEvent.click(screen.getByRole('button', { name: issue.id }));
    expect(screen.getByText(issue.title)).toBeVisible();
    expect(fetch).toHaveBeenCalledTimes(1);
  });

  it('shows loading and a retryable lookup failure', async () => {
    let reject!: (error: Error) => void;
    const fetch = vi.spyOn(api, 'factoryIssues').mockImplementationOnce(() => new Promise((_, fail) => { reject = fail; })).mockResolvedValue([issue]);
    markdown(issue.id);
    fireEvent.mouseEnter(screen.getByRole('button', { name: issue.id }));
    expect(screen.getByRole('status')).toHaveTextContent('Loading issue...');
    await act(async () => reject(new Error('offline')));
    expect(await screen.findByRole('alert')).toHaveTextContent('Could not load issue.');
    await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
    expect(await screen.findByText(issue.title)).toBeVisible();
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('handles unavailable issues and closes after leaving or resizing', async () => {
    vi.spyOn(api, 'factoryIssues').mockResolvedValue([]);
    markdown(issue.id);
    const trigger = screen.getByRole('button', { name: issue.id });
    fireEvent.mouseEnter(trigger);
    expect(await screen.findByText('Issue unavailable.')).toBeVisible();
    fireEvent.mouseLeave(trigger);
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    fireEvent.mouseEnter(trigger);
    fireEvent(window, new Event('resize'));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });
});
