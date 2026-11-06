import { Link, useSearchParams } from 'react-router-dom';
import { Button, SelectField } from './Control';
import { DataTable } from './DataTable';
import { LoadingState } from './LoadingState';
import { EmptyState } from './EmptyState';
import { resources, type ResourceProcess } from '../lib/resources';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';
import { ResourceGraph } from './ResourceGraph';
import { formatMemory as memory, processColor, processKey } from '../lib/resourcePresentation';

export function ResourceViewer() {
  const [params, setParams] = useSearchParams();
  const sort = params.get('resourceSort') ?? 'cpuPercent';
  const selected = params.get('resourceProcess');
  const update = (key: string, value?: string) => setParams((previous) => {
    const next = new URLSearchParams(previous);
    if (value) next.set(key, value);
    else next.delete(key);
    return next;
  });
  const select = (process: ResourceProcess) => update('resourceProcess', processKey(process) === selected ? undefined : processKey(process));
  const query = useVisiblePollingQuery({
    queryKey: ['resources', 'local'], run: resources, intervalMs: 1_000, timeoutMs: 5_000,
  });
  const data = query.data;
  const history = data?.history ?? [];
  const processes = [...new Map([...history.flatMap((point) => point.processes ?? []), ...(data?.processes ?? [])].map((process) => [processKey(process), process])).values()];
  const focused = processes.find((process) => processKey(process) === selected);
  const rows = [...(data?.processes ?? [])].sort((a, b) => {
    if (processKey(a) === selected) return -1;
    if (processKey(b) === selected) return 1;
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
        <ResourceGraph title="CPU" history={history} metric="cpuPercent" processes={processes} selected={selected} onSelect={select} />
        <ResourceGraph title="Memory" history={history} metric="memoryBytes" processes={processes} selected={selected} onSelect={select} />
      </div>
      <div className="resource-toolbar">
        <span>{rows.length} processes · sampled <time dateTime={new Date(data.timestamp).toISOString()}>{new Date(data.timestamp).toLocaleTimeString()}</time></span>
        {selected && <><span>{focused ? `${focused.name} · PID ${focused.pid}` : 'Selected process is no longer available'}</span><Button variant="ghost" size="compact" onClick={() => update('resourceProcess')}>Show all processes</Button></>}
        <SelectField aria-label="Sort processes" value={sort} onChange={(event) => update('resourceSort', event.target.value)}>
          <option value="cpuPercent">CPU, highest first</option>
          <option value="memoryBytes">Memory, highest first</option>
          <option value="pid">PID</option>
          <option value="name">Name</option>
        </SelectField>
      </div>
      {rows.length === 0 ? <EmptyState>No managed processes are running.</EmptyState> : <DataTable framed aria-label="Ocman processes">
        <thead><tr><th>PID</th><th>Parent</th><th>Process</th><th>CPU</th><th>Memory</th></tr></thead>
        <tbody>{rows.map((row) => <tr key={processKey(row)} data-selected={processKey(row) === selected}
          onClick={(event) => { if (!(event.target as HTMLElement).closest('button, a') && !window.getSelection()?.toString()) select(row); }}>
          <td>{row.pid}</td><td>{row.parentPid}</td><td>
            <Button variant="link" size="compact" aria-label={`Select ${row.name}, PID ${row.pid}`} aria-pressed={processKey(row) === selected} onClick={() => select(row)}><span className="resource-process-color" style={{ background: processColor(row) }} aria-hidden="true" />{row.name}</Button>
            {row.command && <div className="resource-process-command">{row.command}</div>}
            {row.sessionId && <Link to={`/session/${encodeURIComponent(row.sessionId)}`}>Open session</Link>}
          </td><td>{row.cpuPercent.toFixed(1)}%</td><td>{memory(row.memoryBytes)}</td>
        </tr>)}</tbody>
      </DataTable>}
    </>}
  </section>;
}
