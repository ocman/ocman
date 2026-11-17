// @vitest-environment jsdom
import { act, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { TOOLTIP_DELAY_MS } from './Tooltip';
import { ResourceGraph } from './ResourceGraph';
import { processColor, processKey } from '../lib/resourcePresentation';
import type { ResourcePoint } from '../lib/resources';

const ocman = { pid: 1, parentPid: 0, name: 'ocman', cpuPercent: 25, memoryBytes: 1024 ** 3 };
const agent = { pid: 2, parentPid: 1, name: 'agent', cpuPercent: 75, memoryBytes: 1024 ** 2 };
const history: ResourcePoint[] = [
  { timestamp: 1000, cpuPercent: 100, memoryBytes: 1025 * 1024 ** 2, processes: [ocman, agent] },
  { timestamp: 2000, cpuPercent: 25, memoryBytes: 1024 ** 3, processes: [ocman] },
];

afterEach(() => { vi.useRealTimers(); });

it('shows process details on hover without selecting the process', () => {
  vi.useFakeTimers();
  const process = { ...ocman, command: '/usr/bin/ocman serve', projectName: 'ocman project', sessionTitle: 'Improve graphs' };
  const select = vi.fn();
  render(<ResourceGraph title="CPU" history={history} metric="cpuPercent" processes={[process]} selected={null} onSelect={select} />);
  const area = screen.getByRole('button', { name: 'CPU: ocman, PID 1' });
  fireEvent.pointerMove(area, { pointerType: 'mouse' });
  act(() => { vi.advanceTimersByTime(TOOLTIP_DELAY_MS); });
  const tooltip = screen.getByRole('tooltip');
  expect(tooltip).toHaveTextContent('ocman');
  expect(tooltip).toHaveTextContent('PID 1');
  expect(tooltip).toHaveTextContent('Parent PID 0');
  expect(tooltip).toHaveTextContent('Latest CPU: 25.0%');
  expect(tooltip).toHaveTextContent('/usr/bin/ocman serve');
  expect(tooltip).toHaveTextContent('ocman project');
  expect(tooltip).toHaveTextContent('Improve graphs');
  expect(area).toHaveAttribute('aria-describedby', tooltip.id);
  expect(select).not.toHaveBeenCalled();
  fireEvent.click(area);
  expect(select).toHaveBeenCalledWith(process);
});

it.each(['cpuPercent', 'memoryBytes'] as const)('shows %s details on keyboard focus and dismisses on Escape', (metric) => {
  render(<ResourceGraph title="Resources" history={history} metric={metric} processes={[ocman, agent]} selected={null} onSelect={vi.fn()} />);
  const area = screen.getByRole('button', { name: 'Resources: ocman, PID 1' });
  fireEvent.focus(area);
  expect(screen.getByRole('tooltip')).toHaveTextContent(metric === 'cpuPercent' ? '25.0%' : '1.00 GiB');
  expect(screen.getByRole('tooltip')).not.toHaveTextContent(/Project:|Session:/);
  fireEvent.keyDown(area, { key: 'Escape' });
  expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
});

it('does not present an exited process value as a current measurement', () => {
  render(<ResourceGraph title="CPU" history={history} metric="cpuPercent" processes={[agent]} selected={null} onSelect={vi.fn()} />);
  fireEvent.focus(screen.getByRole('button', { name: 'CPU: agent, PID 2' }));
  expect(screen.getByRole('tooltip')).toHaveTextContent('Latest CPU: No data');
  fireEvent.blur(screen.getByRole('button'));
  expect(screen.queryByRole('tooltip')).not.toBeInTheDocument();
});

it('stacks actual per-process samples and retains the area of a process that exited', () => {
  const select = vi.fn();
  render(<ResourceGraph title="CPU" history={history} metric="cpuPercent" processes={[ocman, agent]} selected={null} onSelect={select} />);
  const first = screen.getByRole('button', { name: 'CPU: ocman, PID 1' });
  const second = screen.getByRole('button', { name: 'CPU: agent, PID 2' });
  const points = first.querySelector('polygon')!.getAttribute('points')!.split(' ');
  expect(Number(points[0].split(',')[0])).toBeCloseTo(594.0267);
  expect(points[1]).toBe('596,74');
  expect(Number(second.querySelector('circle')!.getAttribute('cx'))).toBeCloseTo(594.0267);
  expect(screen.getByText('Last 5 min')).toBeVisible();
  expect(second.querySelector('circle')).toHaveAttribute('cy', '8');
  expect(first).toHaveAttribute('fill', processColor(ocman));
  fireEvent.keyDown(second, { key: 'Enter' });
  expect(select).toHaveBeenLastCalledWith(agent);
  fireEvent.keyDown(first, { key: ' ' });
  expect(select).toHaveBeenLastCalledWith(ocman);
  fireEvent.keyDown(first, { key: 'ArrowRight' });
  expect(select).toHaveBeenCalledTimes(2);
});

it.each([10, 20])('grows the time window up to fifteen minutes for %i minutes of history', (minutes) => {
  const points = Array.from({ length: minutes * 60 + 1 }, (_, i) => ({ ...history[0], timestamp: i * 1000 }));
  render(<ResourceGraph title="CPU" history={points} metric="cpuPercent" processes={[ocman]} selected={null} onSelect={vi.fn()} />);
  expect(screen.getByText(`Last ${Math.min(minutes, 15)} min`)).toBeVisible();
  const polygon = screen.getByRole('button').querySelector('polygon')!;
  expect(polygon.getAttribute('points')!.split(' ')[0]).toBe('4,74');
  expect(polygon.getAttribute('points')!.split(' ')).toHaveLength((Math.min(minutes, 15) * 60 + 1) * 2);
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
    { ...history[0], timestamp: 15000 },
  ];
  render(<ResourceGraph title="CPU" history={points} metric="cpuPercent" processes={[agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  const area = screen.getByRole('button', { name: 'CPU: agent, PID 2' });
  expect(area.querySelectorAll('polygon')).toHaveLength(1);
  expect(area.querySelectorAll('circle')).toHaveLength(1);
  expect(area.querySelector('polygon')?.getAttribute('points')?.split(' ')).toHaveLength(6);
});

it.each(['cpuPercent', 'memoryBytes'] as const)('connects %s samples up to ten seconds apart but leaves longer gaps blank', (metric) => {
  const points = [1000, 11000, 21001, 23000, 25000, 31001].map((timestamp, i) => ({
    ...history[i === 3 || i === 4 ? 1 : 0], timestamp,
  }));
  render(<ResourceGraph title="Resources" history={points} metric={metric} processes={[agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  const area = screen.getByRole('button', { name: 'Resources: agent, PID 2' });
  const polygons = area.querySelectorAll('polygon');
  expect(polygons).toHaveLength(2);
  expect(area.querySelectorAll('circle')).toHaveLength(0);
  // Both a collection gap and missing process samples connect at exactly ten seconds.
  expect(polygons[0].getAttribute('points')?.split(' ')).toHaveLength(4);
  expect(polygons[1].getAttribute('points')?.split(' ')).toHaveLength(4);
});

it('does not draw processes without samples or report missing values as zero', () => {
  render(<ResourceGraph title="CPU" history={[history[1]]} metric="cpuPercent" processes={[agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  expect(screen.queryByRole('button')).not.toBeInTheDocument();
  expect(screen.getByText('No data')).toBeVisible();
});

it.each(['cpuPercent', 'memoryBytes'] as const)('keeps %s continuous when visible polling samples arrive more than three seconds apart', (metric) => {
  const points = [1000, 4200, 7400, 10600].map((timestamp) => ({ ...history[0], timestamp }));
  render(<ResourceGraph title="Resources" history={points} metric={metric} processes={[agent]} selected={null} onSelect={vi.fn()} />);
  const area = screen.getByRole('button', { name: 'Resources: agent, PID 2' });
  expect(area.querySelectorAll('polygon')).toHaveLength(1);
  expect(area.querySelectorAll('circle')).toHaveLength(0);
});
