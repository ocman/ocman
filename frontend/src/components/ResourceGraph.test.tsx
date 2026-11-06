// @vitest-environment jsdom
import { fireEvent, render, screen } from '@testing-library/react';
import { expect, it, vi } from 'vitest';
import { ResourceGraph } from './ResourceGraph';
import { processColor, processKey } from '../lib/resourcePresentation';
import type { ResourcePoint } from '../lib/resources';

const ocman = { pid: 1, parentPid: 0, name: 'ocman', cpuPercent: 25, memoryBytes: 1024 ** 3 };
const agent = { pid: 2, parentPid: 1, name: 'agent', cpuPercent: 75, memoryBytes: 1024 ** 2 };
const history: ResourcePoint[] = [
  { timestamp: 1000, cpuPercent: 100, memoryBytes: 1025 * 1024 ** 2, processes: [ocman, agent] },
  { timestamp: 2000, cpuPercent: 25, memoryBytes: 1024 ** 3, processes: [ocman] },
];

it('stacks actual per-process samples and retains the area of a process that exited', () => {
  const select = vi.fn();
  render(<ResourceGraph title="CPU" history={history} metric="cpuPercent" processes={[ocman, agent]} selected={null} onSelect={select} />);
  const first = screen.getByRole('button', { name: 'CPU: ocman, PID 1' });
  const second = screen.getByRole('button', { name: 'CPU: agent, PID 2' });
  expect(first.querySelector('polygon')).toHaveAttribute('points', '4,74 596,74 596,96 4,96');
  expect(second.querySelector('circle')).toHaveAttribute('cx', '4');
  expect(second.querySelector('circle')).toHaveAttribute('cy', '8');
  expect(first).toHaveAttribute('fill', processColor(ocman));
  fireEvent.keyDown(second, { key: 'Enter' });
  expect(select).toHaveBeenLastCalledWith(agent);
  fireEvent.keyDown(first, { key: ' ' });
  expect(select).toHaveBeenLastCalledWith(ocman);
  fireEvent.keyDown(first, { key: 'ArrowRight' });
  expect(select).toHaveBeenCalledTimes(2);
});

it('isolates memory values and renders a single sample as a selectable dot', () => {
  render(<ResourceGraph title="Memory" history={[history[0]]} metric="memoryBytes" processes={[ocman, agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  const area = screen.getByRole('button', { name: 'Memory: agent, PID 2' });
  expect(area.querySelector('polygon')).toBeNull();
  expect(area.querySelector('circle')).toHaveAttribute('cy', '8');
  expect(screen.queryByRole('button', { name: 'Memory: ocman, PID 1' })).not.toBeInTheDocument();
  expect(screen.getByText('1.0 MiB')).toBeVisible();
});

it('keeps process identities separate when a PID changes parent or executable', () => {
  expect(processKey(ocman)).not.toEqual(processKey({ ...ocman, parentPid: 99 }));
  expect(processKey(ocman)).not.toEqual(processKey({ ...ocman, name: 'replacement' }));
});

it('connects short missing-sample gaps but leaves longer collection gaps blank', () => {
  const points = [
    { ...history[0], timestamp: 1000 },
    { ...history[0], timestamp: 2000 },
    { ...history[1], timestamp: 3000 },
    { ...history[0], timestamp: 4000 },
    { ...history[0], timestamp: 10000 },
  ];
  render(<ResourceGraph title="CPU" history={points} metric="cpuPercent" processes={[agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  const area = screen.getByRole('button', { name: 'CPU: agent, PID 2' });
  expect(area.querySelectorAll('polygon')).toHaveLength(1);
  expect(area.querySelectorAll('circle')).toHaveLength(1);
  expect(area.querySelector('polygon')?.getAttribute('points')?.split(' ')).toHaveLength(6);
});

it.each(['cpuPercent', 'memoryBytes'] as const)('connects %s samples only when at most three intervals apart', (metric) => {
  const points = [1000, 4000, 7001, 8000, 9000, 10001].map((timestamp, i) => ({
    ...history[i === 3 || i === 4 ? 1 : 0], timestamp,
  }));
  render(<ResourceGraph title="Resources" history={points} metric={metric} processes={[agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  const area = screen.getByRole('button', { name: 'Resources: agent, PID 2' });
  const polygons = area.querySelectorAll('polygon');
  expect(polygons).toHaveLength(2);
  expect(area.querySelectorAll('circle')).toHaveLength(0);
  // Both a collection gap and missing process samples connect at exactly 3 seconds.
  expect(polygons[0].getAttribute('points')?.split(' ')).toHaveLength(4);
  expect(polygons[1].getAttribute('points')?.split(' ')).toHaveLength(4);
});

it('does not draw processes without samples or report missing values as zero', () => {
  render(<ResourceGraph title="CPU" history={[history[1]]} metric="cpuPercent" processes={[agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  expect(screen.queryByRole('button')).not.toBeInTheDocument();
  expect(screen.getByText('No data')).toBeVisible();
});
