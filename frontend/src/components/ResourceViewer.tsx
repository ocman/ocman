import { useState } from 'react';
import { Button, SelectField } from './Control';
import { DataTable } from './DataTable';
import { LoadingState } from './LoadingState';
import { EmptyState } from './EmptyState';
import { resources, type ResourcePoint } from '../lib/resources';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';

function memory(bytes: number) {
  return bytes >= 1024 ** 3 ? `${(bytes / 1024 ** 3).toFixed(2)} GiB` : `${(bytes / 1024 ** 2).toFixed(1)} MiB`;
}

function ResourceGraph({ title, history, metric }: { title: string; history: ResourcePoint[]; metric: 'cpuPercent' | 'memoryBytes' }) {
  const values = history.map((point) => point[metric]);
  const ceiling = Math.max(metric === 'cpuPercent' ? 100 : 1024 ** 2, ...values);
  const format = metric === 'cpuPercent' ? (value: number) => `${value.toFixed(1)}%` : memory;
  const start = history[0]?.timestamp ?? 0;
  const duration = Math.max(59_000, (history.at(-1)?.timestamp ?? start) - start);
  const points = history.map((point) => `${4 + (point.timestamp - start) / duration * 592},${96 - point[metric] / ceiling * 88}`).join(' ');
  return <figure className="resource-graph">
    <figcaption><span>{title}</span><strong>{format(values.at(-1) ?? 0)}</strong></figcaption>
    <svg viewBox="0 0 600 100" role="img" aria-label={`${title} history`} preserveAspectRatio="none">
      <line x1="4" x2="596" y1="96" y2="96" />
      <polyline points={points} />
    </svg>
    <div className="resource-graph-scale"><span>Recent samples</span><span>0 – {format(ceiling)}</span></div>
  </figure>;
}

export function ResourceViewer() {
  const [sort, setSort] = useState('cpuPercent');
  const query = useVisiblePollingQuery({
    queryKey: ['resources', 'local'], run: resources, intervalMs: 1_000, timeoutMs: 5_000,
  });
  const data = query.data;
  const rows = [...(data?.processes ?? [])].sort((a, b) => {
    if (sort === 'name') return a.name.localeCompare(b.name) || a.pid - b.pid;
    if (sort === 'pid') return a.pid - b.pid;
    return (sort === 'memoryBytes' ? b.memoryBytes - a.memoryBytes : b.cpuPercent - a.cpuPercent) || a.pid - b.pid;
  });
  return <section className="resource-viewer" aria-label="Live resources">
    <p>This machine · refreshes every second while visible. Includes ocman, managed agents, browser terminals and their child processes. CPU: 100% is one core. Memory is resident memory, summed per process.</p>
    {query.isPending && !query.error && <LoadingState>Loading resources…</LoadingState>}
    {query.error && <div role="alert">{data ? 'Resource refresh failed. Showing the last sample.' : 'Could not load process resources.'} <Button variant="ghost" onClick={() => void query.refetch()}>Retry</Button></div>}
    {data && <>
      <div className="resource-graphs">
        <ResourceGraph title="CPU" history={data.history} metric="cpuPercent" />
        <ResourceGraph title="Memory" history={data.history} metric="memoryBytes" />
      </div>
      <div className="resource-toolbar">
        <span>{rows.length} processes · sampled <time dateTime={new Date(data.timestamp).toISOString()}>{new Date(data.timestamp).toLocaleTimeString()}</time></span>
        <SelectField aria-label="Sort processes" value={sort} onChange={(event) => setSort(event.target.value)}>
          <option value="cpuPercent">CPU, highest first</option>
          <option value="memoryBytes">Memory, highest first</option>
          <option value="pid">PID</option>
          <option value="name">Name</option>
        </SelectField>
      </div>
      {rows.length === 0 ? <EmptyState>No managed processes are running.</EmptyState> : <DataTable framed aria-label="Ocman processes">
        <thead><tr><th>PID</th><th>Parent</th><th>Process</th><th>CPU</th><th>Memory</th></tr></thead>
        <tbody>{rows.map((row) => <tr key={row.pid}>
          <td>{row.pid}</td><td>{row.parentPid}</td><td>{row.name}</td><td>{row.cpuPercent.toFixed(1)}%</td><td>{memory(row.memoryBytes)}</td>
        </tr>)}</tbody>
      </DataTable>}
    </>}
  </section>;
}
