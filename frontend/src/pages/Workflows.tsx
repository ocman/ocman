import { useDeferredValue, useEffect, useRef, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  api,
  type WorkflowArtifact,
  type WorkflowDefinition,
  type WorkflowMapItemRun,
  type WorkflowRun,
  type WorkflowRunDetail,
  type WorkflowValidation,
  type WorkflowVersion,
} from '../lib/api';
import { useWorkflows } from '../lib/useCapabilities';
import { onSseConnect, onWorkflowRunUpdated, onWorkflowTriggerUpdated } from '../lib/useGlobalEvents';
import { usePageTitle } from '../lib/headerContext';
import { WorkflowBuilder, WorkflowRunGraph } from './WorkflowBuilder';
import { Button, SearchField, SelectField } from '../components/Control';
import { Modal } from '../components/Modal';
import { DaguStatus } from '../components/DaguStatus';
import './Workflows.css';

const EXAMPLE = `id: release
name: Release approvals
version: "1"
concurrency: 1
triggers:
  - id: manual
    type: manual
nodes:
  - id: review
    name: Review
    type: approval
  - id: ship
    name: Ship
    type: approval
dependencies:
  - from: review
    to: ship
`;

export function Workflows() {
  usePageTitle('Workflows');
  const enabled = useWorkflows();
  const [searchParams, setSearchParams] = useSearchParams();
  const [source, setSource] = useState(EXAMPLE);
  const [versions, setVersions] = useState<WorkflowVersion[]>([]);
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [selected, setSelected] = useState<WorkflowRunDetail>();
  const selectedID = useRef<string | undefined>(undefined);
  const validationID = useRef(0);
  const [error, setError] = useState('');
  const [validated, setValidated] = useState<WorkflowValidation>();
  const [compareFrom, setCompareFrom] = useState('');
  const [compareTo, setCompareTo] = useState('');
  const view = searchParams.get('view') === 'author' ? 'author' : 'operations';
  const operationsView = searchParams.get('tab') === 'runs' ? 'runs' : 'workflows';
  const query = searchParams.get('q') ?? '';
  const project = searchParams.get('project') ?? '';
  const runState = searchParams.get('state') ?? 'all';
  const runWorkflowID = searchParams.get('workflow') ?? '';
  const showRevisions = searchParams.get('revisions') === '1';
  const editingVersionID = searchParams.get('version') ?? '';
  const editingVersion = versions.find((version) => version.id === editingVersionID);
  const runOpen = Boolean(searchParams.get('run'));
  const deferredQuery = useDeferredValue(query.trim().toLowerCase());
  const latestVersions = Array.from(
    versions
      .reduce((latest, version) => {
        const current = latest.get(version.workflowId);
        if (!current || version.revision > current.revision) latest.set(version.workflowId, version);
        return latest;
      }, new Map<string, WorkflowVersion>())
      .values(),
  );
  const visibleVersions = (showRevisions ? versions : latestVersions).filter(
    (version) =>
      (!project || version.definition.directory === project) &&
      (!deferredQuery || `${version.name} ${version.workflowId}`.toLowerCase().includes(deferredQuery)),
  );
  const visibleRuns = runs.filter(
    (run) =>
      (runState === 'all' || run.state === runState) &&
      (!runWorkflowID || run.workflowId === runWorkflowID) &&
      (!deferredQuery || run.workflowId.toLowerCase().includes(deferredQuery)),
  );
  const workflowOptions = latestVersions.map((version) => [version.workflowId, version.name]);
  const projectOptions = Array.from(
    new Set(
      latestVersions
        .map((version) => version.definition.directory)
        .filter((directory): directory is string => Boolean(directory)),
    ),
  ).sort();
  function updateLocation(values: Record<string, string | undefined>, replace = false) {
    setSearchParams(
      (params) => {
        for (const [key, value] of Object.entries(values)) {
          if (value) params.set(key, value);
          else params.delete(key);
        }
        return params;
      },
      { replace },
    );
  }

  function select(run: WorkflowRunDetail) {
    selectedID.current = run.id;
    setSelected(run);
  }

  function openRun(run: WorkflowRunDetail) {
    select(run);
    updateLocation({ run: run.id });
  }

  async function refresh() {
    const [nextVersions, nextRuns] = await Promise.all([api.workflows.versions(), api.workflows.runs()]);
    setVersions(nextVersions);
    setRuns(nextRuns);
    setCompareFrom((current) => current || nextVersions[1]?.id || nextVersions[0]?.id || '');
    setCompareTo((current) => current || nextVersions[0]?.id || '');
    const id = selectedID.current ?? nextRuns[0]?.id;
    if (id) select(await api.workflows.run(id));
  }

  useEffect(() => {
    if (!enabled) return;
    void refresh().catch((reason) => setError(String(reason)));
    void validate();
    const unsubscribeRun = onWorkflowRunUpdated(() => {
      void refresh().catch((reason) => setError(String(reason)));
    });
    const unsubscribeTrigger = onWorkflowTriggerUpdated(() => {
      void refresh().catch((reason) => setError(String(reason)));
    });
    const unsubscribeConnect = onSseConnect(() => {
      void refresh().catch((reason) => setError(String(reason)));
    });
    return () => {
      unsubscribeRun();
      unsubscribeTrigger();
      unsubscribeConnect();
    };
    // Selection lives in a ref so reconnects refresh it without resubscribing.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [enabled]);

  useEffect(() => {
    selectedID.current = searchParams.get('run') ?? undefined;
    if (!selectedID.current) setSelected(undefined);
  }, [searchParams]);

  useEffect(() => {
    if (!editingVersionID) return;
    const version = versions.find((candidate) => candidate.id === editingVersionID);
    if (!version) return;
    const nextSource = JSON.stringify(version.definition, null, 2);
    setSource(nextSource);
    setValidated({ definition: version.definition, canonicalJson: version.definition, yaml: nextSource });
  }, [editingVersionID, versions]);

  async function validate() {
    const requestID = ++validationID.current;
    setError('');
    try {
      const result = await api.workflows.validate(source);
      if (requestID === validationID.current) setValidated(result);
    } catch (reason) {
      if (requestID !== validationID.current) return;
      setError(validationError(reason));
    }
  }

  function editSource(value: string) {
    validationID.current++;
    setSource(value);
    setValidated(undefined);
    setError('');
  }

  function editSourceLive(value: string) {
    const requestID = ++validationID.current;
    setSource(value);
    setError('');
    void api.workflows
      .validate(value)
      .then((result) => {
        if (requestID === validationID.current) setValidated(result);
      })
      .catch((reason) => {
        if (requestID === validationID.current) setError(validationError(reason));
      });
  }

  function editVersion(version: WorkflowVersion) {
    const nextSource = JSON.stringify(version.definition, null, 2);
    validationID.current++;
    setSource(nextSource);
    setValidated({ definition: version.definition, canonicalJson: version.definition, yaml: nextSource });
    updateLocation({ view: 'author', version: version.id });
    setError('');
  }

  async function newWorkflow() {
    validationID.current++;
    setSource(EXAMPLE);
    setValidated(undefined);
    updateLocation({ view: 'author', version: undefined });
    setError('');
    try {
      setValidated(await api.workflows.validate(EXAMPLE));
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }

  function applyBuilder(definition: WorkflowDefinition) {
    validationID.current++;
    const nextSource = JSON.stringify(definition, null, 2);
    setSource(nextSource);
    setValidated({ definition, canonicalJson: definition, yaml: nextSource });
    setError('');
  }

  async function publish() {
    setError('');
    try {
      await api.workflows.publish(source);
      await refresh();
      updateLocation({ view: undefined, version: undefined });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }

  async function activate(id: string) {
    setError('');
    try {
      await api.workflows.activate(id);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }

  async function deactivate(id: string) {
    setError('');
    try {
      await api.workflows.deactivate(id);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }

  async function archive() {
    if (!editingVersionID || !window.confirm('Archive this workflow? Its run history and artifacts will be kept.'))
      return;
    setError('');
    try {
      await api.workflows.archive(editingVersionID);
      await refresh();
      updateLocation({ view: undefined, version: undefined });
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }

  async function mutate(action: () => Promise<WorkflowRunDetail>) {
    setError('');
    try {
      const run = await action();
      openRun(run);
      await refresh();
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : String(reason));
    }
  }

  if (!enabled)
    return (
      <main className="workflow-page">
        <p>Workflows are unavailable on this host.</p>
      </main>
    );

  return (
    <main className="workflow-page" data-testid="workflows-page">
      <DaguStatus />
      {view === 'operations' ? (
        <>
          <div className="workflow-tabs workflow-operation-tabs" role="tablist" aria-label="Workflow operations">
            <Button
              role="tab"
              variant="muted"
              aria-selected={operationsView === 'workflows'}
              onClick={() => updateLocation({ tab: undefined })}
            >
              Workflows
            </Button>
            <Button
              role="tab"
              variant="muted"
              aria-selected={operationsView === 'runs'}
              onClick={() => updateLocation({ tab: 'runs' })}
            >
              Run history
            </Button>
          </div>
          {error && (
            <p className="workflow-operation-error" role="alert">
              {error}
            </p>
          )}
          {operationsView === 'workflows' && (
            <section className="workflow-discovery" aria-label="Workflow discovery">
              <div className="workflow-filter">
                <label>
                  Find workflows
                  <SearchField
                    aria-label="Find workflows"
                    value={query}
                    onChange={(event) => updateLocation({ q: event.target.value }, true)}
                    placeholder="Name or ID"
                  />
                </label>
                <label>
                  Project
                  <SelectField
                    aria-label="Project"
                    value={project}
                    onChange={(event) => updateLocation({ project: event.target.value })}
                  >
                    <option value="">All projects</option>
                    {projectOptions.map((directory) => (
                      <option key={directory} value={directory}>
                        {projectName(directory)}
                      </option>
                    ))}
                  </SelectField>
                </label>
                <label className="workflow-revision-toggle">
                  <input
                    type="checkbox"
                    checked={showRevisions}
                    onChange={(event) => updateLocation({ revisions: event.target.checked ? '1' : undefined })}
                  />{' '}
                  Show revisions
                </label>
              </div>
              <div className="workflow-list-heading">
                <h2>Workflows</h2>
                <Button variant="accent" onClick={() => void newWorkflow()}>
                  New workflow
                </Button>
              </div>
              {visibleVersions.length > 0 ? (
                <table className="workflow-table">
                  <thead>
                    <tr>
                      <th>Workflow</th>
                      <th>Project</th>
                      <th>Revision</th>
                      <th>Triggers</th>
                      <th></th>
                    </tr>
                  </thead>
                  <tbody>
                    {visibleVersions.map((version) => (
                      <tr key={version.id}>
                        <td>
                          <button
                            type="button"
                            aria-label={version.name}
                            onClick={() => updateLocation({ tab: 'runs', workflow: version.workflowId })}
                          >
                            {version.name}
                            <small>{version.workflowId}</small>
                          </button>
                        </td>
                        <td>
                          {version.definition.directory ? (
                            <span title={version.definition.directory}>
                              {projectName(version.definition.directory)}
                            </span>
                          ) : (
                            <small>No project</small>
                          )}
                        </td>
                        <td>
                          {version.revision} · <State state={version.active ? 'active' : 'inactive'} />
                        </td>
                        <td>
                          {version.triggerStates.length
                            ? version.triggerStates.map((trigger) => (
                                <div className="workflow-trigger-summary" key={trigger.id}>
                                  <strong>
                                    {trigger.type}
                                    {trigger.type === 'cron' && ` (${trigger.cron})`}
                                    {trigger.type === 'interval' && ` (${trigger.intervalSeconds}s)`}
                                  </strong>
                                </div>
                              ))
                            : 'Manual'}
                        </td>
                        <td>
                          <div className="workflow-controls">
                            <Button size="small" aria-label="Edit workflow" onClick={() => editVersion(version)}>
                              <i className="bi bi-pencil" aria-hidden="true" />
                            </Button>
                            {version.active && (
                              <Button
                                size="small"
                                aria-label="Start run"
                                onClick={() => {
                                  updateLocation({ tab: 'runs', workflow: version.workflowId });
                                  void mutate(() => api.workflows.startActive(version.workflowId));
                                }}
                              >
                                <i className="bi bi-play-fill" aria-hidden="true" />
                              </Button>
                            )}
                          </div>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              ) : (
                <p>No workflows match this search.</p>
              )}
            </section>
          )}
          {operationsView === 'runs' && (
            <section className="workflow-run-list" aria-label="Workflow runs">
              <div className="workflow-list-heading">
                <h2>Run history{runWorkflowID && ` · ${runWorkflowID}`}</h2>
              </div>
              <div className="workflow-filter">
                <label>
                  Find workflows
                  <SearchField
                    aria-label="Find workflows"
                    value={query}
                    onChange={(event) => updateLocation({ q: event.target.value }, true)}
                    placeholder="Name or ID"
                  />
                </label>
                <label>
                  Workflow
                  <SelectField
                    aria-label="Workflow"
                    value={runWorkflowID}
                    onChange={(event) => updateLocation({ workflow: event.target.value })}
                  >
                    <option value="">All workflows</option>
                    {workflowOptions.map(([id, name]) => (
                      <option key={id} value={id}>
                        {name} ({id})
                      </option>
                    ))}
                  </SelectField>
                </label>
                <label>
                  Run state
                  <SelectField
                    aria-label="Run state"
                    value={runState}
                    onChange={(event) =>
                      updateLocation({ state: event.target.value === 'all' ? undefined : event.target.value })
                    }
                  >
                    <option value="all">All states</option>
                    <option value="active">Active</option>
                    <option value="paused">Paused</option>
                    <option value="successful">Successful</option>
                    <option value="failed">Failed</option>
                    <option value="canceled">Canceled</option>
                  </SelectField>
                </label>
                {(query || runWorkflowID || runState !== 'all') && (
                  <Button
                    variant="link"
                    className="workflow-clear-filters"
                    onClick={() => updateLocation({ q: undefined, workflow: undefined, state: undefined })}
                  >
                    Clear filters
                  </Button>
                )}
              </div>
              <table className="workflow-table">
                <thead>
                  <tr>
                    <th>Workflow</th>
                    <th>State</th>
                    <th>Started</th>
                    <th>Trigger</th>
                  </tr>
                </thead>
                <tbody>
                  {visibleRuns.map((run) => (
                    <tr key={run.id} data-selected={selected?.id === run.id || undefined}>
                      <td>
                        <button type="button" onClick={() => void api.workflows.run(run.id).then(openRun)}>
                          {run.workflowId}
                          <small>{run.id}</small>
                        </button>
                      </td>
                      <td>
                        <State state={run.state} />
                      </td>
                      <td>{formatTime(run.createdAt)}</td>
                      <td>{run.trigger?.type ?? 'manual'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              {visibleRuns.length === 0 && <p>No runs match these filters.</p>}
            </section>
          )}
          {selected && runOpen && (
            <RunModal
              run={selected}
              targetVersion={versions.find((version) => version.workflowId === selected.workflowId && version.active) ?? selected.version}
              mutate={mutate}
              onClose={() => updateLocation({ run: undefined })}
              onSelectRun={(id) =>
                void api.workflows
                  .run(id)
                  .then(openRun)
                  .catch((reason) => setError(String(reason)))
              }
            />
          )}
        </>
      ) : (
        <>
          <section className="workflow-author" aria-label="Workflow authoring">
            <header className="workflow-author-head">
              <div>
                <span className="workflow-kicker">Immutable workflow revision</span>
                <h2>Author workflow</h2>
                <p>Build and connect steps on the canvas. Source is available for advanced edits.</p>
              </div>
              <div className="workflow-controls">
                <Button variant="muted" onClick={() => void validate()}>
                  Validate
                </Button>
                <Button variant="accent" onClick={() => void publish()}>
                  Save new version
                </Button>
              </div>
            </header>
            {error && <p role="alert">{error}</p>}
            {validated ? (
              <WorkflowBuilder
                definition={validated.definition}
                source={source}
                onChange={applyBuilder}
                onSourceChange={editSourceLive}
              />
            ) : (
              <label className="workflow-yaml">
                Workflow YAML or JSON
                <textarea
                  aria-label="Workflow YAML or JSON"
                  value={source}
                  onChange={(event) => editSource(event.target.value)}
                  spellCheck={false}
                />
              </label>
            )}
            {editingVersionID && (
              <div className="workflow-controls">
                <Button
                  variant={editingVersion?.active ? 'muted' : 'accent'}
                  onClick={() =>
                    void (editingVersion?.active ? deactivate(editingVersionID) : activate(editingVersionID))
                  }
                >
                  {editingVersion?.active ? 'Deactivate' : 'Activate'}
                </Button>
                <Button variant="muted" className="workflow-archive" onClick={() => void archive()}>
                  Delete workflow
                </Button>
              </div>
            )}
          </section>
          <VersionComparison
            versions={versions}
            from={compareFrom}
            to={compareTo}
            onFrom={setCompareFrom}
            onTo={setCompareTo}
          />
        </>
      )}
    </main>
  );
}

function RunModal({
  run,
  targetVersion,
  mutate,
  onClose,
  onSelectRun,
}: {
  run: WorkflowRunDetail;
  targetVersion: WorkflowVersion;
  mutate: (action: () => Promise<WorkflowRunDetail>) => Promise<void>;
  onClose: () => void;
  onSelectRun: (id: string) => void;
}) {
  return (
    <Modal
      onClose={onClose}
      label="Workflow run details"
      backdropClassName="workflow-run-modal-backdrop"
      dialogClassName="workflow-run-modal"
    >
      <button className="workflow-run-modal-close" type="button" onClick={onClose}>
        Close
      </button>
      <RunView run={run} targetVersion={targetVersion} mutate={mutate} onSelectRun={onSelectRun} />
    </Modal>
  );
}

function RunView({
  run,
  targetVersion,
  mutate,
  onSelectRun,
}: {
  run: WorkflowRunDetail;
  targetVersion: WorkflowVersion;
  mutate: (action: () => Promise<WorkflowRunDetail>) => Promise<void>;
  onSelectRun: (id: string) => void;
}) {
  const [artifacts, setArtifacts] = useState<WorkflowArtifact[]>([]);
  const [selectedNodeID, setSelectedNodeID] = useState(run.nodes[0]?.nodeId);
  const inspectorRef = useRef<HTMLElement>(null);
  const selectedNode = run.nodes.find((node) => node.nodeId === selectedNodeID) ?? run.nodes[0];
  const approvals = run.nodes.filter((node) => node.type === 'approval' && node.state === 'ready');
  const running = run.nodes.filter((node) => node.state === 'running');
  function revealNode(id: string) {
    setSelectedNodeID(id);
    inspectorRef.current?.scrollIntoView?.({ behavior: 'smooth', block: 'nearest' });
  }
  useEffect(() => {
    let active = true;
    void api.workflows
      .artifacts(run.id)
      .then((next) => {
        if (active) setArtifacts(next);
      })
      .catch(() => {
        if (active) setArtifacts([]);
      });
    return () => {
      active = false;
    };
  }, [run.id, run.state, run.updatedAt]);
  return (
    <section className="workflow-run" aria-label="Workflow run">
      <header>
        <div>
          <span className="workflow-kicker">{run.id}</span>
          <h2>{run.version.name}</h2>
          <p>
            Revision {run.version.revision} · definition {run.version.definition.version}
          </p>
        </div>
        <div className="workflow-run-actions">
          <span className="workflow-run-status" data-state={run.state}>
            {run.state === 'active' && <span className="workflow-run-spinner" aria-hidden="true" />}
            <State state={run.state} />
          </span>
          <div className="workflow-controls">
            {run.state === 'active' && (
              <button type="button" onClick={() => void mutate(() => api.workflows.pause(run.id))}>
                Pause run
              </button>
            )}
            {(run.state === 'active' || run.state === 'paused') && (
              <button type="button" onClick={() => void mutate(() => api.workflows.cancel(run.id))}>
                Cancel run
              </button>
            )}
          </div>
        </div>
      </header>
      {run.parentRunId && (
        <p className="workflow-run-parent">
          Mapped item <strong>{run.itemKey}</strong> of{' '}
          <button type="button" onClick={() => onSelectRun(run.parentRunId!)}>
            parent run {run.parentNodeId}
          </button>
        </p>
      )}
      {run.retryOfRunId && (
        <p className="workflow-run-parent">
          Retried from <strong>{run.retryFromNodeId}</strong> in{' '}
          <button type="button" onClick={() => onSelectRun(run.retryOfRunId!)}>
            run {run.retryOfRunId}
          </button>
        </p>
      )}
      {run.trigger && (
        <p className="workflow-run-trigger">
          <strong>
            {run.trigger.id} · {run.trigger.type} · {run.trigger.overlap ?? 'skip'}
          </strong>{' '}
          · {run.trigger.detail} · fired {formatTime(run.trigger.firedAt)}
        </p>
      )}
      <section className="workflow-run-activity" aria-label="Run activity">
        <div data-kind="approval">
          <strong>Needs approval <span>{approvals.length}</span></strong>
          {approvals.length ? approvals.map((node) => (
            <button key={node.nodeId} type="button" aria-label={`View approval ${node.name}`} onClick={() => revealNode(node.nodeId)}>
              {node.name}<small>{node.nodeId}</small>
            </button>
          )) : <small>None</small>}
        </div>
        <div data-kind="running">
          <strong>Running now <span>{running.length}</span></strong>
          {running.length ? running.map((node) => (
            <button key={node.nodeId} type="button" aria-label={`View running node ${node.name}`} onClick={() => revealNode(node.nodeId)}>
              {node.name}<small>{node.nodeId}</small>
            </button>
          )) : <small>None</small>}
        </div>
      </section>
      {run.resources && run.resources.length > 0 && (
        <section className="workflow-resources" aria-label="Resource pools">
          {run.resources.map((pool) => (
            <div className="workflow-resource" key={pool.pool || 'run'}>
              <span>{pool.pool || 'Run capacity'}</span>
              <strong>
                {pool.held === 0 ? `${pool.capacity} available` : `${pool.held} of ${pool.capacity} in use`}
              </strong>
              {pool.waiting && pool.waiting.length > 0 && <small>Waiting: {pool.waiting.join(', ')}</small>}
            </div>
          ))}
        </section>
      )}
      {run.workspace && run.workspace.length > 0 && (
        <ul className="workflow-leases" aria-label="Workspace leases">
          {run.workspace.map((lease) => (
            <li key={lease.nodeId}>
              <strong>{lease.nodeId}</strong>: shard {lease.shard} · {lease.mode}
              {lease.commit && ' (commit)'}
              {lease.paths && lease.paths.length > 0 && <span> · {lease.paths.join(', ')}</span>}
              {lease.host && <span> · host {lease.host}</span>}
              {lease.shardPath && <span> · {lease.shardPath}</span>}
            </li>
          ))}
        </ul>
      )}
      <div className="workflow-run-layout">
        <WorkflowRunGraph
          definition={run.version.definition}
          runs={run.nodes}
          selectedID={selectedNode?.nodeId}
          onSelect={setSelectedNodeID}
        />
        <aside ref={inspectorRef} className="workflow-run-inspector" aria-label="Selected node details">
          {selectedNode ? (
            <RunNode run={run} node={selectedNode} targetVersion={targetVersion} mutate={mutate} onSelectRun={onSelectRun} />
          ) : (
            <p>No nodes in this run.</p>
          )}
        </aside>
      </div>
      <ArtifactList runId={run.id} artifacts={artifacts} />
    </section>
  );
}

function VersionComparison({
  versions,
  from,
  to,
  onFrom,
  onTo,
}: {
  versions: WorkflowVersion[];
  from: string;
  to: string;
  onFrom: (id: string) => void;
  onTo: (id: string) => void;
}) {
  if (!versions.length) return null;
  const left = versions.find((version) => version.id === from) ?? versions[0];
  const revisions = versions.filter((version) => version.workflowId === left.workflowId);
  const right =
    revisions.find((version) => version.id === to && version.id !== left.id) ??
    revisions.find((version) => version.id !== left.id);
  return (
    <details className="workflow-comparison" aria-label="Version comparison">
      <summary>Compare revisions{revisions.length > 1 ? ` · ${left.name}` : ''}</summary>
      {revisions.length < 2 ? (
        <p>Publish another revision of {left.name} to compare it.</p>
      ) : (
        <section className="workflow-run">
          <div className="workflow-controls">
            <label>
              Compare from
              <select aria-label="Compare from" value={left.id} onChange={(event) => onFrom(event.target.value)}>
                {revisions.map((version) => (
                  <option key={version.id} value={version.id}>
                    Revision {version.revision}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Compare to
              <select aria-label="Compare to" value={right!.id} onChange={(event) => onTo(event.target.value)}>
                {revisions
                  .filter((version) => version.id !== left.id)
                  .map((version) => (
                    <option key={version.id} value={version.id}>
                      Revision {version.revision}
                    </option>
                  ))}
              </select>
            </label>
          </div>
          <div className="workflow-history">
            <pre>
              Revision {left.revision}
              {'\n'}
              {JSON.stringify(left.definition, null, 2)}
            </pre>
            <pre>
              Revision {right!.revision}
              {'\n'}
              {JSON.stringify(right!.definition, null, 2)}
            </pre>
          </div>
        </section>
      )}
    </details>
  );
}

function RunNode({
  run,
  node,
  targetVersion,
  mutate,
  onSelectRun,
}: {
  run: WorkflowRunDetail;
  node: WorkflowRunDetail['nodes'][number];
  targetVersion: WorkflowVersion;
  mutate: (action: () => Promise<WorkflowRunDetail>) => Promise<void>;
  onSelectRun: (id: string) => void;
}) {
  const attempt = node.attempts.at(-1);
  const phase = run.nodes.findIndex((candidate) => candidate.nodeId === node.nodeId) + 1;
  const definition = run.version.definition.nodes.find((candidate) => candidate.id === node.nodeId);
  const lease = run.workspace?.find((candidate) => candidate.nodeId === node.nodeId);
  const conditions = (run.version.definition.dependencies ?? [])
    .filter((edge) => edge.to === node.nodeId && edge.condition)
    .map((edge) => edge.condition!);
  const attemptLine = attempt
    ? `Attempt ${attempt.seq}: ${attempt.state}${attempt.exitCode !== undefined && attempt.exitCode >= 0 ? ` (exit ${attempt.exitCode})` : ''}`
    : 'Not attempted';
  const retrySupported = !run.version.definition.workspace && !targetVersion.definition.workspace &&
    !run.version.definition.nodes.some((candidate) => candidate.type === 'map' || candidate.type === 'join') &&
    !targetVersion.definition.nodes.some((candidate) => candidate.type === 'map' || candidate.type === 'join');
  return (
    <article data-state={node.state}>
      <small>
        Phase {phase} · {node.type} · {node.nodeId}
      </small>
      <h3>{node.name}</h3>
      <State state={node.state} />
      {node.pinnedVersionId && <p>Pinned subworkflow: {node.pinnedVersionId}</p>}
      {definition?.resources?.length ? (
        <p>Resources: {definition.resources.map((resource) => `${resource.pool} x${resource.units}`).join(', ')}</p>
      ) : null}
      {definition?.lease && (
        <p>
          Workspace request: {definition.lease.mode ?? 'exclusive'}
          {definition.lease.paths?.length ? ` · ${definition.lease.paths.join(', ')}` : ''}
          {definition.lease.commit ? ' · commit coordinator' : ''}
          {lease?.shardPath ? ` · active shard ${lease.shardPath}` : ''}
        </p>
      )}
      <p>{attemptLine}</p>
      {conditions.map((condition) => (
        <p key={condition} className="workflow-condition">
          Condition {node.state === 'skipped' ? 'skipped' : 'evaluated'}: <code>{condition}</code>
        </p>
      ))}
      {node.attempts.length > 1 && (
        <p className="workflow-repeat-history">
          Repeat history: {node.attempts.map((item) => `#${item.seq} ${item.state}`).join(', ')}
        </p>
      )}
      {attempt?.resolvedAt && (
        <p>
          Resolved by {attempt.resolvedBy || 'user'} · {formatTime(attempt.resolvedAt)}
        </p>
      )}
      {attempt?.reusedAttemptId && <p>Reused attempt {attempt.reusedAttemptId}</p>}
      {node.type !== 'map' && node.type !== 'join' && node.result.output !== null && (
        <pre aria-label="node output">{JSON.stringify(node.result.output, null, 2)}</pre>
      )}
      {node.type === 'agent' && <AgentNode attempt={attempt} />}
      {attempt?.state === 'unknown' && run.state === 'paused' && (
        <div className="workflow-controls">
          <button
            type="button"
            onClick={() => void mutate(() => api.workflows.resolveUnknown(run.id, attempt.id, 'successful'))}
          >
            Mark succeeded
          </button>
          <button
            type="button"
            onClick={() => void mutate(() => api.workflows.resolveUnknown(run.id, attempt.id, 'failed'))}
          >
            Mark failed
          </button>
          <button
            type="button"
            onClick={() => void mutate(() => api.workflows.resolveUnknown(run.id, attempt.id, 'retry'))}
          >
            Retry safely
          </button>
        </div>
      )}
      {node.type === 'approval' && node.state === 'ready' && run.state === 'active' && (
        <button type="button" onClick={() => void mutate(() => api.workflows.approve(run.id, node.nodeId))}>
          Approve {node.name}
        </button>
      )}
      {(run.state === 'successful' || run.state === 'failed') && retrySupported && (
        <button
          type="button"
          onClick={() => {
            if (window.confirm(`Retry ${node.name} and its descendants on revision ${targetVersion.revision}? This may repeat external side effects.`))
              void mutate(() => api.workflows.retryFrom(run.id, node.nodeId, targetVersion.id));
          }}
        >
          Retry from {node.name} on revision {targetVersion.revision}
        </button>
      )}
      {attempt && node.type === 'command' && <CommandAttempt attempt={attempt} />}
      {node.type === 'map' && (
        <MapNode
          items={(run.children ?? []).filter((item) => item.mapNode === node.nodeId)}
          output={node.result.output}
          error={node.attempts.map((a) => a.error).find(Boolean)}
          onSelectRun={onSelectRun}
        />
      )}
      {node.type === 'join' && <JoinNode output={node.result.output} />}
    </article>
  );
}

function AgentNode({ attempt }: { attempt?: WorkflowRunDetail['nodes'][number]['attempts'][number] }) {
  if (!attempt) return null;
  return (
    <>
      {attempt.sessionId && (
        <>
          <Link to={`/session/${encodeURIComponent(attempt.sessionId)}`}>Open agent session</Link>
          <p>Session: {attempt.sessionState}</p>
        </>
      )}
      {attempt.error && <p role="alert">{attempt.error}</p>}
    </>
  );
}

function MapNode({
  items,
  output,
  error,
  onSelectRun,
}: {
  items: WorkflowMapItemRun[];
  output: unknown;
  error?: string;
  onSelectRun: (id: string) => void;
}) {
  const [expanded, setExpanded] = useState(false);
  const counts = items.reduce<Record<string, number>>((acc, item) => {
    acc[item.state] = (acc[item.state] ?? 0) + 1;
    return acc;
  }, {});
  const summary =
    Object.entries(counts)
      .map(([state, count]) => `${count} ${state}`)
      .join(', ') || 'no items';
  const results =
    output && typeof output === 'object' && 'items' in output
      ? ((output as { items?: { key: string; output: unknown }[] }).items ?? [])
      : [];
  return (
    <div className="workflow-map">
      {error && <p role="alert">{error}</p>}
      <button
        type="button"
        className="workflow-map-toggle"
        aria-expanded={expanded}
        onClick={() => setExpanded((value) => !value)}
      >
        {expanded ? 'Collapse' : 'Expand'} {items.length} mapped item{items.length === 1 ? '' : 's'}{' '}
        <small>({summary})</small>
      </button>
      {expanded && (
        <ul className="workflow-map-items" data-testid="workflow-map-items">
          {items.map((item) => (
            <li key={item.key} data-state={item.state} data-testid="workflow-map-item">
              <strong>{item.key}</strong>{' '}
              <small>
                #{item.index} · <State state={item.state} />
              </small>
              {item.childRunId && (
                <button type="button" onClick={() => onSelectRun(item.childRunId!)}>
                  Open item run
                </button>
              )}
              {results.find((result) => result.key === item.key)?.output != null && (
                <pre aria-label={`${item.key} output`}>
                  {JSON.stringify(results.find((result) => result.key === item.key)!.output, null, 2)}
                </pre>
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function JoinNode({ output: raw }: { output: unknown }) {
  if (!raw || typeof raw !== 'object') return null;
  const result = raw as {
    policy?: string;
    success?: number;
    failed?: number;
    total?: number;
    error?: string;
    items?: { key: string; status: string; index: number; output?: unknown }[];
  };
  return (
    <div className="workflow-join" data-testid="workflow-join">
      {result.error && <p role="alert">{result.error}</p>}
      <p>
        <strong>{result.policy}</strong> · {result.success ?? 0}/{result.total ?? 0} succeeded
      </p>
      <ol className="workflow-join-items">
        {(result.items ?? []).map((item) => (
          <li key={item.key} data-state={item.status}>
            {item.key}: <State state={item.status} />
            {item.output != null && (
              <pre aria-label={`${item.key} joined output`}>{JSON.stringify(item.output, null, 2)}</pre>
            )}
          </li>
        ))}
      </ol>
    </div>
  );
}

function ArtifactList({ runId, artifacts }: { runId: string; artifacts: WorkflowArtifact[] }) {
  if (artifacts.length === 0) return null;
  return (
    <section className="workflow-artifacts" aria-label="Historical workflow artifacts">
      <h3>Historical artifacts</h3>
      <p>Auxiliary inputs and files from older workflow versions. Node output is shown in its Node Result.</p>
      <ul>
        {artifacts.map((artifact) => (
          <li key={artifact.id} data-testid="workflow-artifact">
            <strong>{artifact.name}</strong>{' '}
            <small>
              {artifact.kind} · {formatSize(artifact.size)} · {artifact.nodeId} attempt {artifact.attemptId}
              {artifact.expiresAt ? ` · expires ${formatTime(artifact.expiresAt)}` : ' · retained'}
            </small>
            {artifact.payloadAvailable ? (
              <a href={api.workflows.artifactDownloadUrl(runId, artifact.id)} download>
                Download
              </a>
            ) : (
              <span className="workflow-artifact-gone">Payload cleaned up</span>
            )}
          </li>
        ))}
      </ul>
    </section>
  );
}

function State({ state }: { state: string }) {
  return <strong data-state={state}>{state}</strong>;
}

function projectName(directory: string) {
  return directory.replace(/\/+$/, '').split('/').at(-1) || directory;
}

function validationError(reason: unknown) {
  const message = reason instanceof Error ? reason.message : String(reason);
  const hint = message.includes('duplicate key')
    ? 'Remove or rename the duplicate key.'
    : message.includes('requires directory')
      ? 'Set the workflow or trigger directory to an existing absolute project path.'
      : message.includes('requires')
        ? 'Fill in the required setting named in the error.'
        : message.includes('invalid workflow source')
          ? 'Check the YAML or JSON syntax and field types.'
          : 'Review the named workflow setting and try again.';
  return `Validation failed: ${message} Hint: ${hint}`;
}

function formatSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function CommandAttempt({ attempt }: { attempt: WorkflowRunDetail['nodes'][number]['attempts'][number] }) {
  return (
    <div className="workflow-command-log">
      {attempt.error && (
        <p>
          <b>Error</b>: {attempt.error}
        </p>
      )}
      {attempt.stdout && (
        <pre aria-label="stdout">
          {attempt.stdout}
          {attempt.stdoutTruncated && '\n[truncated]'}
        </pre>
      )}
      {attempt.stderr && (
        <pre aria-label="stderr">
          {attempt.stderr}
          {attempt.stderrTruncated && '\n[truncated]'}
        </pre>
      )}
    </div>
  );
}

function formatTime(value?: number) {
  return value ? new Date(value).toLocaleString() : 'not scheduled';
}
