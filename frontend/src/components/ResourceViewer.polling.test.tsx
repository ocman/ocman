// @vitest-environment jsdom
import { act, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { MemoryRouter } from 'react-router-dom';
import { renderWithQuery } from '../test/renderWithQuery';
import { resources, type ResourcePoint, type ResourceSnapshot } from '../lib/resources';
import { ResourceViewer } from './ResourceViewer';

vi.mock('../lib/resources', () => ({ resources: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { systemStats: vi.fn(async () => ({ uptime: 60 })) } }));
vi.mock('../lib/usePanelVisible', () => ({ useDocumentVisible: () => true }));
vi.mock('../lib/settingsQueries', () => ({ useRemotes: () => ({ data: [] }) }));

afterEach(() => vi.useRealTimers());

it('retains and connects every sample while the visible page polls a slow resource endpoint', async () => {
  vi.useFakeTimers();
  const history: ResourcePoint[] = [];
  const processes = [{ pid: 1, parentPid: 0, name: 'ocman', cpuPercent: 25, memoryBytes: 1024 }];
  vi.mocked(resources).mockImplementation(() => new Promise((resolve) => {
    setTimeout(() => {
      const point = { timestamp: Date.now(), cpuPercent: 25, memoryBytes: 1024, processes };
      history.push(point);
      resolve({ ...point, processes, history: [...history] });
    }, 1200);
  }));
  const page = renderWithQuery(<MemoryRouter><ResourceViewer /></MemoryRouter>);
  await act(() => vi.advanceTimersByTimeAsync(1200));
  for (let i = 0; i < 3; i++) await act(() => vi.advanceTimersByTimeAsync(3200));
  await act(() => vi.advanceTimersByTimeAsync(1));

  expect(resources).toHaveBeenCalledTimes(4);
  const cached = page.client.getQueryData<ResourceSnapshot>(['resources', 'local'])!;
  expect(cached.history).toHaveLength(4);
  expect(cached.history[1].timestamp - cached.history[0].timestamp).toBe(3200);
  for (const title of ['CPU', 'Memory']) {
    const area = screen.getByRole('button', { name: `${title}: ocman, PID 1` });
    expect(area.querySelectorAll('polygon')).toHaveLength(1);
    expect(area.querySelectorAll('circle')).toHaveLength(0);
    expect(area.querySelector('polygon')?.getAttribute('points')?.split(' ')).toHaveLength(8);
  }
  page.unmount();
});
