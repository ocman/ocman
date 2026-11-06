import type { ResourcePoint, ResourceProcess } from '../lib/resources';
import { processKey, processColor, formatMemory } from '../lib/resourcePresentation';

export function ResourceGraph({ title, history, metric, processes, selected, onSelect }: {
  title: string; history: ResourcePoint[]; metric: 'cpuPercent' | 'memoryBytes';
  processes: ResourceProcess[]; selected: string | null; onSelect: (process: ResourceProcess) => void;
}) {
  const visible = processes.filter((process) => !selected || processKey(process) === selected);
  const samples = history.map((point) => new Map((point.processes ?? []).map((process) => [processKey(process), process[metric]])));
  const values = history.map((point, i) => selected ? samples[i].get(selected) : point[metric]);
  const ceiling = Math.max(metric === 'cpuPercent' ? 100 : 1024 ** 2, ...values.filter((value) => value !== undefined));
  const format = metric === 'cpuPercent' ? (value: number) => `${value.toFixed(1)}%` : formatMemory;
  const start = history[0]?.timestamp ?? 0;
  const duration = Math.max(1_000, (history.at(-1)?.timestamp ?? start) - start);
  const x = (index: number) => 4 + ((history[index].timestamp - start) / duration) * 592;
  const y = (value: number) => 96 - value / ceiling * 88;
  const baseline = history.map(() => 0);
  const areas = visible.map((process) => {
    const bottom = [...baseline];
    const key = processKey(process);
    const top = samples.map((sample, i) => baseline[i] += sample.get(key) ?? 0);
    const segments: number[][] = [];
    samples.forEach((sample, i) => {
      if (!sample.has(key)) return;
      // Sampling is once per second; never interpolate across a missed sample.
      const previous = segments.at(-1);
      if (previous?.at(-1) === i - 1 && history[i].timestamp - history[i - 1].timestamp <= 1500) previous.push(i);
      else segments.push([i]);
    });
    if (!segments.length) return null;
    const latest = samples.at(-1)?.get(key);
    return <g key={key} fill={processColor(process)}
      role="button" tabIndex={0} aria-label={`${title}: ${process.name}, PID ${process.pid}`}
      aria-pressed={selected === processKey(process)} onClick={() => onSelect(process)}
      onKeyDown={(event) => { if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); onSelect(process); } }}>
      <title>{process.name} · PID {process.pid} · {latest === undefined ? 'No data' : format(latest)}</title>
      {segments.map((indices) => indices.length === 1
        ? <circle key={indices[0]} cx={x(indices[0])} cy={y(top[indices[0]])} r="2" />
        : <polygon key={indices[0]} points={[...indices.map((i) => `${x(i)},${y(top[i])}`), ...indices.map((i) => `${x(i)},${y(bottom[i])}`).reverse()].join(' ')} />)}
    </g>;
  });
  return <figure className="resource-graph">
    <figcaption><span>{title}</span><strong>{values.at(-1) === undefined ? 'No data' : format(values.at(-1)!)}</strong></figcaption>
    <svg viewBox="0 0 600 100" role="group" aria-label={`${title} history`} preserveAspectRatio="none">
      <line x1="4" x2="596" y1="96" y2="96" />
      {areas}
    </svg>
    <div className="resource-graph-scale"><span>Recent samples</span><span>0 – {format(ceiling)}</span></div>
  </figure>;
}
