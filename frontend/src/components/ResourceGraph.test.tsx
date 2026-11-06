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
  expect(first).toHaveAttribute('points', '4,74 596,74 596,96 4,96');
  expect(second).toHaveAttribute('points', '4,8 596,74 596,74 4,74');
  expect(first).toHaveAttribute('fill', processColor(ocman));
  fireEvent.keyDown(second, { key: 'Enter' });
  expect(select).toHaveBeenLastCalledWith(agent);
  fireEvent.keyDown(first, { key: ' ' });
  expect(select).toHaveBeenLastCalledWith(ocman);
  fireEvent.keyDown(first, { key: 'ArrowRight' });
  expect(select).toHaveBeenCalledTimes(2);
});

it('isolates memory values and renders a single sample as a selectable area', () => {
  render(<ResourceGraph title="Memory" history={[history[0]]} metric="memoryBytes" processes={[ocman, agent]} selected={processKey(agent)} onSelect={vi.fn()} />);
  expect(screen.getByRole('button', { name: 'Memory: agent, PID 2' })).toHaveAttribute('points', '4,8 596,8 596,96 4,96');
  expect(screen.queryByRole('button', { name: 'Memory: ocman, PID 1' })).not.toBeInTheDocument();
  expect(screen.getByText('1.0 MiB')).toBeVisible();
});

it('keeps process identities separate when a PID changes parent or executable', () => {
  expect(processKey(ocman)).not.toEqual(processKey({ ...ocman, parentPid: 99 }));
  expect(processKey(ocman)).not.toEqual(processKey({ ...ocman, name: 'replacement' }));
});
