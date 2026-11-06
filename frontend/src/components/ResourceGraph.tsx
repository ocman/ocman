import type { ResourcePoint, ResourceProcess } from '../lib/resources';
import { processKey, processColor, formatMemory } from '../lib/resourcePresentation';

export function ResourceGraph({ title, history, metric, processes, selected, onSelect }: {
  title: string; history: ResourcePoint[]; metric: 'cpuPercent' | 'memoryBytes';
  processes: ResourceProcess[]; selected: string | null; onSelect: (process: ResourceProcess) => void;
}) {
  const visible = processes.filter((process) => !selected || processKey(process) === selected);
  const samples = history.map((point) => new Map((point.processes ?? []).map((process) => [processKey(process), process[metric]])));
  const values = history.map((point, i) => selected ? samples[i].get(selected) ?? 0 : point[metric]);
  const ceiling = Math.max(metric === 'cpuPercent' ? 100 : 1024 ** 2, ...values);
  const format = metric === 'cpuPercent' ? (value: number) => `${value.toFixed(1)}%` : formatMemory;
  const start = history[0]?.timestamp ?? 0;
  const duration = Math.max(1_000, (history.at(-1)?.timestamp ?? start) - start);
  const x = (index: number) => 4 + ((history[index].timestamp - start) / duration) * 592;
  const y = (value: number) => 96 - value / ceiling * 88;
  const baseline = history.map(() => 0);
  const areas = visible.map((process) => {
    const bottom = [...baseline];
    const top = samples.map((sample, i) => baseline[i] += sample.get(processKey(process)) ?? 0);
    const points = history.length === 1
      ? `4,${y(top[0])} 596,${y(top[0])} 596,${y(bottom[0])} 4,${y(bottom[0])}`
      : [...top.map((value, i) => `${x(i)},${y(value)}`), ...bottom.map((value, i) => `${x(i)},${y(value)}`).reverse()].join(' ');
    return <polygon key={processKey(process)} points={points} fill={processColor(process)}
      role="button" tabIndex={0} aria-label={`${title}: ${process.name}, PID ${process.pid}`}
      aria-pressed={selected === processKey(process)} onClick={() => onSelect(process)}
      onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onSelect(process); } }}>
      <title>{process.name} · PID {process.pid} · {format(samples.at(-1)?.get(processKey(process)) ?? 0)}</title>
    </polygon>;
  });
  return <figure className="resource-graph">
    <figcaption><span>{title}</span><strong>{format(values.at(-1) ?? 0)}</strong></figcaption>
    <svg viewBox="0 0 600 100" role="group" aria-label={`${title} history`} preserveAspectRatio="none">
      <line x1="4" x2="596" y1="96" y2="96" />
      {areas}
    </svg>
    <div className="resource-graph-scale"><span>Recent samples</span><span>0 – {format(ceiling)}</span></div>
  </figure>;
}
