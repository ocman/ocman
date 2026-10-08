// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, expect, it, vi } from 'vitest';
import { FactorySessionRecovery } from './FactorySessionRecovery';
import { api } from '../lib/api';

afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

it('keeps unsent recovery guidance while hidden without polling', async () => {
  vi.useFakeTimers();
  let hidden = false;
  vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
  const epics = [{ id: 'epic', attempts: [{ id: 'attempt', session: { platform: 'opencode', id: 'session' } }] }];
  const issues = [{ id: 'gate', recovery: { issueId: 'gate', epicId: 'epic', attemptId: 'attempt', workId: 'work',
    question: 'Next step?', reason: 'Need guidance', choices: [], resolution: 'open' } }];
  const loadEpics = vi.spyOn(api, 'factoryEpics').mockResolvedValue(epics as never);
  const loadIssues = vi.spyOn(api, 'factoryIssues').mockResolvedValue(issues as never);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  client.setQueryData(['factory-epics'], epics);
  client.setQueryData(['factory-epics', 'epic', 'issues'], issues);
  render(<QueryClientProvider client={client}><MemoryRouter><FactorySessionRecovery platformID="opencode" sessionID="session" /></MemoryRouter></QueryClientProvider>);
  await act(async () => { await vi.advanceTimersByTimeAsync(0); });
  fireEvent.change(screen.getByLabelText('Recovery response'), { target: { value: 'Keep my unsent guidance' } });
  loadEpics.mockClear(); loadIssues.mockClear();
  act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
  expect(screen.getByLabelText('Recovery response')).toHaveValue('Keep my unsent guidance');
  await act(async () => { await vi.advanceTimersByTimeAsync(30_000); });
  expect(loadEpics).not.toHaveBeenCalled(); expect(loadIssues).not.toHaveBeenCalled();
  await act(async () => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); await vi.advanceTimersByTimeAsync(0); });
  expect(screen.getByLabelText('Recovery response')).toHaveValue('Keep my unsent guidance');
});
