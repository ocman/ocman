import { useState } from 'react';
import { useQueryClient } from '@tanstack/react-query';
import { Link, useSearchParams } from 'react-router-dom';
import { Button, SelectField } from './Control';
import { DataTable } from './DataTable';
import { LoadingState } from './LoadingState';
import { EmptyState } from './EmptyState';
import { resources, type ResourceProcess, type ResourceSnapshot } from '../lib/resources';
import { useVisiblePollingQuery } from '../lib/useVisiblePollingQuery';
import { ResourceGraph } from './ResourceGraph';
import { SegmentedControl } from './SegmentedControl';
import { resourceTree } from '../lib/resourceTree';
import { attributeResourceHistory } from '../lib/resourceAttribution';
import { formatMemory as memory, processColor, processKey } from '../lib/resourcePresentation';

export function ResourceViewer() {
  const client = useQueryClient();
  const [params, setParams] = useSearchParams();
  const [pausedSample, setPausedSample] = useState<ResourceSnapshot | null>(null);
  const sort = params.get('resourceSort') ?? 'cpuPercent';
  const tree = params.get('resourceView') === 'tree';
  const selected = params.get('resourceProcess');
  const project = params.get('resourceProject') ?? '';
  const session = params.get('resourceSession') ?? '';
  const filtered = Boolean(project || session);
  const update = (key: string, value?: string) => setParams((previous) => {
    const next = new URLSearchParams(previous);
    if (value) next.set(key, value);
    else next.delete(key);
    if (key === 'resourceProject' || key === 'resourceSession') next.delete('resourceProcess');
    return next;
  });
  const clearFilters = () => setParams((previous) => {
    const next = new URLSearchParams(previous);
    next.delete('resourceProject');
    next.delete('resourceSession');
    next.delete('resourceProcess');
    return next;
  });
  const select = (process: ResourceProcess) => update('resourceProcess', processKey(process) === selected ? undefined : processKey(process));
  const query = useVisiblePollingQuery({
    queryKey: ['resources', 'local'],
    run: async (signal) => attributeResourceHistory(await resources(signal), client.getQueryData<ResourceSnapshot>(['resources', 'local'])),
    intervalMs: 2_000, timeoutMs: 5_000,
  });
  const data = query.data;
  const listData = pausedSample ?? data;
  const allHistory = data?.history ?? [];
  const allProcesses = [...new Map([...allHistory.flatMap((point) => point.processes ?? []), ...(listData?.processes ?? []), ...(data?.processes ?? [])].map((process) => [processKey(process), process])).values()];
  const matches = (process: ResourceProcess) => (!project || process.projectName === project) && (!session || process.sessionId === session);
  const processes = allProcesses.filter(matches);
  const projects = [...new Set([...allProcesses.flatMap((process) => process.projectName ? [process.projectName] : []), ...(project ? [project] : [])])].sort((a, b) => a.localeCompare(b));
  const sessions = new Map<string, string>();
  for (const process of allProcesses) {
    if (process.sessionId) sessions.set(process.sessionId, process.sessionTitle || sessions.get(process.sessionId) || process.sessionId);
  }
  if (session && !sessions.has(session)) sessions.set(session, session);
  const history = filtered ? allHistory.map((point) => {
    const scoped = point.processes?.filter(matches);
    return { ...point, processes: scoped, cpuPercent: scoped?.reduce((sum, process) => sum + process.cpuPercent, 0) ?? 0, memoryBytes: scoped?.reduce((sum, process) => sum + process.memoryBytes, 0) ?? 0 };
  }) : allHistory;
  const focused = processes.find((process) => processKey(process) === selected);
  const rows = (listData?.processes ?? []).filter(matches).sort((a, b) => {
    if (processKey(a) === selected) return -1;
    if (processKey(b) === selected) return 1;
    if (sort === 'name') return a.name.localeCompare(b.name) || a.pid - b.pid;
    if (sort === 'pid') return a.pid - b.pid;
    return (sort === 'memoryBytes' ? b.memoryBytes - a.memoryBytes : b.cpuPercent - a.cpuPercent) || a.pid - b.pid;
  });
  const displayedRows = tree ? resourceTree(rows) : rows.map((process) => ({ process, depth: 0 }));
  return <section className="resource-viewer" aria-label="Live resources">
    <p>This machine · refreshes every 2 seconds while visible. Includes ocman, managed agents, browser terminals and their child processes. CPU: 100% is one core. Memory is resident memory, summed per process.</p>
    {query.isPending && !query.error && <LoadingState>Loading resources…</LoadingState>}
    {query.error && <div role="alert">{data ? 'Resource refresh failed. Showing the last sample.' : 'Could not load process resources.'} <Button variant="ghost" onClick={() => void query.refetch()}>Retry</Button></div>}
    {data && listData && <>
      <div className="resource-toolbar">
        <SelectField aria-label="Filter by project" value={project} onChange={(event) => update('resourceProject', event.target.value)}>
          <option value="">All projects</option>
          {projects.map((name) => <option key={name} value={name}>{name}</option>)}
        </SelectField>
        <SelectField aria-label="Filter by session" value={session} onChange={(event) => update('resourceSession', event.target.value)}>
          <option value="">All sessions</option>
          {[...sessions].sort((a, b) => a[1].localeCompare(b[1])).map(([id, title]) => <option key={id} value={id}>{title}</option>)}
        </SelectField>
        {filtered && <Button variant="ghost" size="compact" onClick={clearFilters}>Clear filters</Button>}
      </div>
      <div className="resource-graphs">
        <ResourceGraph title="CPU" history={history} metric="cpuPercent" processes={processes} selected={selected} onSelect={select} />
        <ResourceGraph title="Memory" history={history} metric="memoryBytes" processes={processes} selected={selected} onSelect={select} />
      </div>
      <div className="resource-toolbar">
        <span>{rows.length} processes · sampled <time dateTime={new Date(listData.timestamp).toISOString()}>{new Date(listData.timestamp).toLocaleTimeString()}</time>{pausedSample && ' · List updates paused'}</span>
        <Button variant="ghost" size="compact" onClick={() => setPausedSample(pausedSample ? null : data)}>{pausedSample ? 'Resume list updates' : 'Pause list updates'}</Button>
        <SegmentedControl label="Process view" value={tree ? 'tree' : 'flat'} options={[{ value: 'flat', label: 'Flat' }, { value: 'tree', label: 'Tree' }]} onChange={(value) => update('resourceView', value === 'tree' ? 'tree' : undefined)} />
        {selected && <><span>{focused ? `${focused.name} · PID ${focused.pid}` : 'Selected process is no longer available'}</span><Button variant="ghost" size="compact" onClick={() => update('resourceProcess')}>Show all processes</Button></>}
        <SelectField aria-label="Sort processes" value={sort} onChange={(event) => update('resourceSort', event.target.value)}>
          <option value="cpuPercent">CPU, highest first</option>
          <option value="memoryBytes">Memory, highest first</option>
          <option value="pid">PID</option>
          <option value="name">Name</option>
        </SelectField>
      </div>
      {rows.length === 0 ? <EmptyState>{filtered ? 'No processes match these filters.' : 'No managed processes are running.'}</EmptyState> : <DataTable framed aria-label="Ocman processes">
        <thead><tr><th>PID</th><th>Parent</th><th>Process</th><th>Session</th><th>Project</th><th>CPU</th><th>Memory</th></tr></thead>
        <tbody>{displayedRows.map(({ process: row, depth }) => <tr key={processKey(row)} data-selected={processKey(row) === selected} data-depth={tree ? depth : undefined}
          onClick={(event) => { if (!(event.target as HTMLElement).closest('button, a') && !window.getSelection()?.toString()) select(row); }}>
          <td>{row.pid}</td><td>{row.parentPid}</td><td><div className="resource-process-details" style={{ paddingInlineStart: `${depth * 20}px` }}>
            {depth > 0 && <span className="resource-process-branch" aria-hidden="true">└─</span>}
            <Button variant="link" size="compact" aria-label={`Select ${row.name}, PID ${row.pid}`} aria-pressed={processKey(row) === selected} onClick={() => select(row)}><span className="resource-process-color" style={{ background: processColor(row) }} aria-hidden="true" />{row.name}</Button>
            {row.command && <div className="resource-process-command">{row.command}</div>}
          </div></td><td>{row.sessionId ? <Link to={`/session/${encodeURIComponent(row.sessionId)}`}>{row.sessionTitle || 'Open session'}</Link> : '—'}</td><td>{row.projectName || '—'}</td><td>{row.cpuPercent.toFixed(1)}%</td><td>{memory(row.memoryBytes)}</td>
        </tr>)}</tbody>
      </DataTable>}
    </>}
  </section>;
}
