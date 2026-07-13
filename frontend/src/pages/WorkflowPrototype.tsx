import { useRef, useState } from 'react';
import type { RefObject } from 'react';
import { usePageTitle } from '../lib/headerContext';
import './WorkflowPrototype.css';

type NodeState = 'pending' | 'ready' | 'running' | 'waiting' | 'successful' | 'failed' | 'skipped' | 'blocked' | 'unknown' | 'paused' | 'canceled';

type FixtureNode = {
  id: string;
  title: string;
  kind: string;
  state: NodeState;
  duration: string;
  attempts: string[];
  logs: string[];
  artifacts: string[];
  resources: string[];
  workspace: string[];
};

const STATE_META: Record<NodeState, { label: string; symbol: string }> = {
  pending: { label: 'Pending', symbol: '[ ]' },
  ready: { label: 'Ready', symbol: '>>' },
  running: { label: 'Running', symbol: 'RUN' },
  waiting: { label: 'Waiting', symbol: 'WAIT' },
  successful: { label: 'Successful', symbol: 'OK' },
  failed: { label: 'Failed', symbol: '!!' },
  skipped: { label: 'Skipped', symbol: '--' },
  blocked: { label: 'Blocked', symbol: 'X' },
  unknown: { label: 'Unknown', symbol: '?' },
  paused: { label: 'Paused', symbol: '||' },
  canceled: { label: 'Canceled', symbol: 'STOP' },
};

const ITEM_NAMES = [
  'parser.ts', 'lexer.ts', 'resolver.ts', 'bundler.ts', 'transpiler.ts', 'printer.ts',
  'runtime.ts', 'shell.ts', 'install.ts', 'lockfile.ts', 'manifest.ts', 'http.ts',
  'websocket.ts', 'sqlite.ts', 'ffi.ts', 'watcher.ts', 'test-runner.ts', 'cli.ts',
];

const PIPELINE = [
  ['Implement', 'agent'],
  ['Review safety', 'agent'],
  ['Review parity', 'agent'],
  ['Join findings', 'join'],
  ['Fix findings', 'agent'],
  ['Validate', 'command'],
] as const;

const ITEM_STATES: NodeState[] = ['successful', 'running', 'waiting', 'failed', 'blocked', 'ready', 'pending', 'unknown', 'paused', 'canceled', 'skipped'];
const MAPPED_NODE_COUNT = ITEM_NAMES.length * (PIPELINE.length + 3);
const TOTAL_NODE_COUNT = MAPPED_NODE_COUNT + 4;

const YAML_SOURCE = `name: bun-zig-to-rust
version: 7
concurrency: 24
resources:
  agents: { capacity: 12 }
  compilers: { capacity: 3 }
  git-writer: { capacity: 1 }
nodes:
  discover:
    type: command
    run: scripts/discover-migration-units
    outputs: [migration-units]
  migrate:
    type: map
    needs: [discover]
    items: artifact://discover/migration-units
    key: item.path
    workflow: migrate-unit@4
    max_parallel: 12
  integration_tests:
    type: command
    needs: [migrate]
    resources: { compilers: 1 }
  serialized_commit:
    type: command
    needs: [integration_tests]
    resources: { git-writer: 1 }
`;

const JSON_SOURCE = `{
  "name": "bun-zig-to-rust",
  "version": 7,
  "concurrency": 24,
  "resources": {
    "agents": { "capacity": 12 },
    "compilers": { "capacity": 3 },
    "git-writer": { "capacity": 1 }
  },
  "nodes": {
    "discover": { "type": "command", "outputs": ["migration-units"] },
    "migrate": { "type": "map", "needs": ["discover"], "workflow": "migrate-unit@4", "max_parallel": 12 },
    "integration_tests": { "type": "command", "needs": ["migrate"] },
    "serialized_commit": { "type": "command", "needs": ["integration_tests"] }
  }
}`;

function fixtureNode(id: string, title: string, kind: string, state: NodeState, item?: string): FixtureNode {
  const failed = state === 'failed';
  const attempts: Record<NodeState, string[]> = {
    pending: ['Not attempted · 2 dependencies incomplete'],
    ready: ['Ready · dependencies satisfied · awaiting scheduler'],
    running: ['#2 active · 08:42', '#1 retry · provider overload · 00:19'],
    waiting: ['Not attempted · resource request queued'],
    successful: ['#1 successful · exit 0 · 02:18'],
    failed: ['#3 failed · exit 1 · 02:18', '#2 failed · output contract · 01:44', '#1 timed out · 10:00'],
    skipped: ['Not attempted · upstream branch skipped'],
    blocked: ['Not attempted · failed dependency blocks scheduling'],
    unknown: ['#1 unknown · executor disconnected after side effect'],
    paused: ['Not attempted · run scheduling paused'],
    canceled: ['#1 canceled · termination acknowledged · 00:31'],
  };
  return {
    id,
    title,
    kind,
    state,
    duration: state === 'running' ? '08:42 elapsed' : state === 'waiting' ? 'waiting 03:14' : ['successful', 'failed', 'canceled'].includes(state) ? '02:18' : 'not started',
    attempts: attempts[state],
    logs: failed ? ['cargo test parser', 'error[E0308]: mismatched types at src/parser.rs:418'] : ['Applied translation guidance', 'Running focused tests: parser::comments parser::precedence'],
    artifacts: [`diff · ${item ?? 'repository'}.patch · 18.4 KB`, 'result · summary.json · 2.1 KB', 'diagnostics · cargo-check.txt · 7.8 KB'],
    resources: state === 'waiting' ? ['agents 12/12 · queued #3', 'compilers 3/3 · queued #1'] : ['agents 1/12 · lease active', 'compilers 0/3'],
    workspace: [`shard · wt-migrate-0${(id.length % 4) + 1}`, `lease · ${item ? `src/${item.replace('.ts', '.rs')}` : 'exclusive repository'}`, 'host · local'],
  };
}

function pipelineState(itemState: NodeState, stepIndex: number): NodeState {
  if (itemState === 'successful' || itemState === 'pending' || itemState === 'skipped') return itemState;
  if (itemState === 'failed') return stepIndex < 2 ? 'successful' : stepIndex === 2 ? 'failed' : 'blocked';
  if (itemState === 'running') return stepIndex < 2 ? 'successful' : stepIndex === 2 ? 'running' : 'pending';
  if (itemState === 'waiting') return stepIndex === 0 ? 'successful' : stepIndex === 1 ? 'waiting' : 'pending';
  if (stepIndex > 0) return itemState === 'canceled' ? 'skipped' : itemState === 'unknown' ? 'blocked' : 'pending';
  return itemState;
}

const DISCOVER = fixtureNode('discover', 'Discover migration units', 'command', 'successful');
const INTEGRATION = fixtureNode('integration', 'Integration test campaign', 'command', 'waiting');
const COMMIT = fixtureNode('commit', 'Serialized commit', 'command', 'blocked');

function StatusBadge({ state }: { state: NodeState }) {
  const meta = STATE_META[state];
  return (
    <span className="wf-status" data-state={state} aria-label={`State: ${meta.label}`}>
      <b aria-hidden="true">{meta.symbol}</b>{meta.label}
    </span>
  );
}

function NodeCard({ node, selected, onSelect }: { node: FixtureNode; selected: boolean; onSelect: () => void }) {
  return (
    <button
      type="button"
      className={`wf-node${selected ? ' is-selected' : ''}`}
      data-state={node.state}
      aria-label={`Inspect ${node.title}, ${STATE_META[node.state].label}`}
      aria-pressed={selected}
      onClick={onSelect}
    >
      <span className="wf-node-kind">{node.kind}</span>
      <strong>{node.title}</strong>
      <span className="wf-node-meta"><StatusBadge state={node.state} /> <span>{node.duration}</span></span>
    </button>
  );
}

function NodeInspector({ node, containerRef }: { node: FixtureNode; containerRef: RefObject<HTMLElement | null> }) {
  return (
    <aside className="wf-inspector" aria-label="Node details" ref={containerRef} tabIndex={-1}>
      <div className="wf-inspector-head">
        <div><span className="wf-eyebrow">{node.kind} · {node.id}</span><h2>{node.title}</h2></div>
        <StatusBadge state={node.state} />
      </div>
      <section><h3>Attempts</h3>{node.attempts.map((value) => <p key={value} className="wf-detail-row">{value}</p>)}</section>
      <section><h3>Logs</h3><pre>{node.logs.join('\n')}</pre></section>
      <section><h3>Artifacts</h3>{node.artifacts.map((value) => <p key={value} className="wf-detail-row wf-linkish">{value}</p>)}</section>
      <section><h3>Resources</h3>{node.resources.map((value) => <p key={value} className="wf-detail-row">{value}</p>)}</section>
      <section><h3>Workspace ownership</h3>{node.workspace.map((value) => <p key={value} className="wf-detail-row">{value}</p>)}</section>
    </aside>
  );
}

function SourceEditor({
  language,
  source,
  changed,
  onLanguageChange,
  onSourceChange,
}: {
  language: 'yaml' | 'json';
  source: string;
  changed: boolean;
  onLanguageChange: (language: 'yaml' | 'json') => void;
  onSourceChange: (source: string) => void;
}) {
  return (
    <section className="wf-source" aria-label="Workflow definition editor">
      <div className="wf-source-toolbar">
        <div className="wf-segmented" aria-label="Source format">
          <button type="button" aria-pressed={language === 'yaml'} onClick={() => onLanguageChange('yaml')}>YAML</button>
          <button type="button" aria-pressed={language === 'json'} onClick={() => onLanguageChange('json')}>JSON</button>
        </div>
        <span>{changed ? 'Edited local draft · validation not wired' : 'Fixture source · not persisted'}</span>
      </div>
      <div className="wf-source-grid">
        <label className="wf-editor">
          <span>Workflow source</span>
          <textarea aria-label="Workflow source" spellCheck={false} value={source} onChange={(event) => onSourceChange(event.target.value)} />
        </label>
        <aside className="wf-definition-notes">
          <span className="wf-eyebrow">Normalized preview</span>
          <h2>Migration campaign v7</h2>
          <dl><dt>Static nodes</dt><dd>4</dd><dt>Mapped subworkflow</dt><dd>migrate-unit@4</dd><dt>Expansion</dt><dd>18 items · {MAPPED_NODE_COUNT} descendants</dd><dt>Critical resource</dt><dd>git-writer · capacity 1</dd></dl>
          <div className="wf-validation"><b>{changed ? 'DRAFT' : 'FIXTURE'}</b><span>{changed ? 'validation intentionally not wired' : 'known-valid example'}</span></div>
          <p>Graph editing is intentionally absent. The run view stays pinned to version 7 and does not pretend to reflect local draft edits.</p>
        </aside>
      </div>
    </section>
  );
}

export function WorkflowPrototype() {
  usePageTitle('Workflow tracer lab');
  const [view, setView] = useState<'source' | 'graph'>('source');
  const [language, setLanguage] = useState<'yaml' | 'json'>('yaml');
  const [sources, setSources] = useState({ yaml: YAML_SOURCE, json: JSON_SOURCE });
  const [sourceChanged, setSourceChanged] = useState(false);
  const [mapExpanded, setMapExpanded] = useState(false);
  const [expandedItem, setExpandedItem] = useState<string>();
  const [selected, setSelected] = useState<FixtureNode>(DISCOVER);
  const inspectorRef = useRef<HTMLElement>(null);
  const selectedInsideCollapsedMap = !mapExpanded && selected.id.startsWith('item:');

  function itemNode(item: string, itemIndex: number, stepIndex: number) {
    const [step, kind] = PIPELINE[stepIndex];
    const state = pipelineState(ITEM_STATES[itemIndex % ITEM_STATES.length], stepIndex);
    return fixtureNode(`item:${item}:${stepIndex}`, `${step} ${item}`, kind, state, item);
  }

  function jumpToFailure() {
    const item = ITEM_NAMES[3];
    setMapExpanded(true);
    setExpandedItem(item);
    setSelected(itemNode(item, 3, 2));
    requestAnimationFrame(() => {
      inspectorRef.current?.focus();
      inspectorRef.current?.scrollIntoView({ block: 'nearest' });
    });
  }

  return (
    <main className="wf-page" data-testid="workflow-prototype">
      <header className="wf-page-head">
        <div>
          <span className="wf-kicker">Disposable tracer · fixture data only</span>
          <h1>Workflow tracer lab</h1>
          <p>Author the immutable definition, then inspect a {TOTAL_NODE_COUNT}-node migration run without a production workflow API.</p>
        </div>
        <div className="wf-run-summary" aria-label="Fixture run summary">
          <span><b>RUN-0241</b> · version 7</span><span>{TOTAL_NODE_COUNT} node runs</span><span>24 max concurrency</span>
        </div>
      </header>

      <div className="wf-view-tabs" aria-label="Workflow prototype view">
        <button type="button" aria-pressed={view === 'source'} onClick={() => setView('source')}>Definition source</button>
        <button type="button" aria-pressed={view === 'graph'} onClick={() => setView('graph')}>Run graph</button>
      </div>

      {view === 'source' ? (
        <SourceEditor
          language={language}
          source={sources[language]}
          changed={sourceChanged}
          onLanguageChange={setLanguage}
          onSourceChange={(source) => {
            setSources({ ...sources, [language]: source });
            setSourceChanged(true);
          }}
        />
      ) : (
        <section className="wf-run-view">
          <div className="wf-graph-toolbar">
            <ul className="wf-legend" aria-label="Node state legend">
              {(Object.keys(STATE_META) as NodeState[]).map((state) => <li key={state}><StatusBadge state={state} /></li>)}
            </ul>
            <button type="button" className="wf-jump" onClick={jumpToFailure}>Jump to failed</button>
          </div>
          <div className="wf-run-layout">
            <div className="wf-graph-scroll" role="region" aria-label="Workflow run graph" tabIndex={0}>
              <div className="wf-canvas">
                <div className="wf-stage">
                  <span className="wf-stage-label">01 · Discover</span>
                  <NodeCard node={DISCOVER} selected={selected.id === DISCOVER.id} onSelect={() => setSelected(DISCOVER)} />
                </div>
                <div className="wf-arrow" aria-hidden="true">-&gt;</div>
                <section className="wf-map" aria-label="Mapped migration items">
                  <div className="wf-map-head">
                    <div><span className="wf-stage-label">02 · Dynamic map</span><h2>Migration units</h2><p>18 stable keys · migrate-unit@4 · 12 parallel</p></div>
                    <button type="button" onClick={() => setMapExpanded(!mapExpanded)} aria-expanded={mapExpanded}>
                      {mapExpanded ? 'Collapse' : 'Expand'} 18 migration items
                    </button>
                  </div>
                  {!mapExpanded ? (
                    <div className="wf-map-collapsed">
                      <span>18 branches / {MAPPED_NODE_COUNT} descendant node runs</span>
                      <ul className="wf-map-distribution" aria-label="Collapsed map outcome distribution">
                        {(Object.keys(STATE_META) as NodeState[]).map((state) => {
                          const count = ITEM_NAMES.filter((_, index) => ITEM_STATES[index % ITEM_STATES.length] === state).length;
                          return count > 0 ? <li key={state}><StatusBadge state={state} /><span>{count}</span></li> : null;
                        })}
                      </ul>
                      {selectedInsideCollapsedMap && <b>Selection retained inside collapsed map</b>}
                    </div>
                  ) : (
                    <div className="wf-map-expanded">
                      <nav className="wf-item-list" aria-label="Migration item branches">
                        {ITEM_NAMES.map((item, index) => (
                          <button
                            type="button"
                            key={item}
                            aria-label={`${expandedItem === item ? 'Collapse' : 'Expand'} ${item} branch, ${STATE_META[ITEM_STATES[index % ITEM_STATES.length]].label}`}
                            aria-expanded={expandedItem === item}
                            onClick={() => setExpandedItem(expandedItem === item ? undefined : item)}
                          >
                            <span>{expandedItem === item ? '-' : '+'}</span><b>{item}</b><StatusBadge state={ITEM_STATES[index % ITEM_STATES.length]} />
                          </button>
                        ))}
                      </nav>
                      {expandedItem ? (
                        <div className="wf-branch" aria-label={`${expandedItem} mapped branch`}>
                          {PIPELINE.map((_, stepIndex) => {
                            const itemIndex = ITEM_NAMES.indexOf(expandedItem);
                            const node = itemNode(expandedItem, itemIndex, stepIndex);
                            return (
                              <div className="wf-branch-step" key={node.id}>
                                <NodeCard node={node} selected={selected.id === node.id} onSelect={() => setSelected(node)} />
                                {stepIndex < PIPELINE.length - 1 && <span aria-hidden="true">-&gt;</span>}
                                {stepIndex === PIPELINE.length - 1 && (
                                  <div className="wf-nested-map">
                                    <b>Nested test map</b>
                                    {['unit', 'integration', 'parity'].map((shard, shardIndex) => {
                                      const shardState = pipelineState(ITEM_STATES[itemIndex % ITEM_STATES.length], stepIndex + shardIndex);
                                      const shardNode = fixtureNode(`item:${expandedItem}:test:${shard}`, `${shard} test shard ${expandedItem}`, 'mapped command', shardState, expandedItem);
                                      return <button type="button" key={shard} onClick={() => setSelected(shardNode)} aria-label={`Inspect ${shardNode.title}, ${STATE_META[shardState].label}`} aria-pressed={selected.id === shardNode.id}><StatusBadge state={shardState} />{shard}</button>;
                                    })}
                                  </div>
                                )}
                              </div>
                            );
                          })}
                        </div>
                      ) : <p className="wf-branch-placeholder">Expand an item to inspect its nested implement, dual-review, fix, and test graph.</p>}
                    </div>
                  )}
                </section>
                <div className="wf-arrow" aria-hidden="true">-&gt;</div>
                <div className="wf-stage">
                  <span className="wf-stage-label">03 · Fan-in</span>
                  <NodeCard node={INTEGRATION} selected={selected.id === INTEGRATION.id} onSelect={() => setSelected(INTEGRATION)} />
                  <span className="wf-stage-label wf-stage-gap">04 · Git gate</span>
                  <NodeCard node={COMMIT} selected={selected.id === COMMIT.id} onSelect={() => setSelected(COMMIT)} />
                </div>
              </div>
            </div>
            <NodeInspector node={selected} containerRef={inspectorRef} />
          </div>
        </section>
      )}
    </main>
  );
}
