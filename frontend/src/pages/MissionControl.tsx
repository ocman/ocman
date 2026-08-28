import { useRef, useState, type FormEvent } from 'react';
import { Button } from '../components/Control';
import { useAddFactoryPlanningWork, useCompleteFactoryPlanningWork, useCreateWorkEpic, useDecideFactoryPlan, useFactoryFormulaActions, useFactoryFormulas, useFactoryStatus, useMutateFactoryPlan, useWorkEpics } from '../lib/queries';
import type { FactoryPlan, FactoryPlanGraph } from '../lib/api';
import type { FactoryDispatchItem, FactoryDispatchState } from '../lib/api.types';
import './MissionControl.css';

function CreateWorkEpic() {
  const createEpic = useCreateWorkEpic();
  const formulas = useFactoryFormulas();
  const [projectError, setProjectError] = useState('');
  const pendingInstantiation = useRef<{ key: string; id: string } | undefined>(undefined);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const formElement = event.currentTarget;
    const form = new FormData(formElement);
    const initialProject = String(form.get('initialProject') ?? '').trim();
    if (!initialProject.startsWith('/')) {
      setProjectError('Initial project must be an absolute path.');
      return;
    }
    setProjectError('');
    const goal = String(form.get('goal') ?? '').trim();
    const selectedFormula = String(form.get('formula') ?? '');
    const [formulaId, revisionText] = selectedFormula.split('@');
    const formulaRevision = revisionText ? Number(revisionText) : undefined;
    const key = JSON.stringify([goal, initialProject, formulaId, formulaRevision]);
    if (pendingInstantiation.current?.key !== key) {
      pendingInstantiation.current = { key, id: crypto.randomUUID() };
    }
    await createEpic.mutateAsync({
      instantiationId: pendingInstantiation.current.id,
      goal,
      initialProject,
      acknowledgeLocalExecution: true,
      formulaId: formulaId || undefined,
      formulaRevision,
    }).then(() => {
      pendingInstantiation.current = undefined;
      formElement.reset();
    }, () => undefined);
  }

  return (
    <section className="factory-panel" aria-labelledby="factory-create-heading">
      <h3 id="factory-create-heading">Create Work Epic</h3>
      <form className="factory-create-form" aria-labelledby="factory-create-heading" onSubmit={(event) => void submit(event)}>
        <fieldset disabled={createEpic.isPending}>
          <label>
            Formula
            <select name="formula" disabled={formulas.isLoading}>
              {(formulas.data ?? []).filter((formula) => !formula.archived).flatMap((formula) => (
                (formula.revisions.length ? formula.revisions : [{ revision: formula.currentRevision, instantiable: true }]).filter((revision) => revision.instantiable).map((revision) => (
                  <option key={`${formula.id}@${revision.revision}`} value={`${formula.id}@${revision.revision}`}>
                    {formula.name} ({formula.id} r{revision.revision})
                  </option>
                ))
              ))}
            </select>
          </label>
          <label>Goal<textarea name="goal" required /></label>
          <label>
            Initial project
            <input
              name="initialProject"
              required
              aria-invalid={Boolean(projectError)}
              aria-describedby={projectError ? 'factory-project-error' : undefined}
              placeholder="/absolute/path/to/project"
            />
          </label>
          {projectError && <span id="factory-project-error" role="alert">{projectError}</span>}
          <label className="factory-acknowledgement">
            <input name="acknowledgement" type="checkbox" required />
            I understand this work executes locally without isolation.
          </label>
          <Button type="submit" variant="accent">{createEpic.isPending ? 'Creating…' : 'Create Work Epic'}</Button>
        </fieldset>
      </form>
      {createEpic.isError && (
        <div className="oc-error-banner" role="alert">
          {createEpic.error instanceof Error ? createEpic.error.message : 'Could not create Work Epic.'}
        </div>
      )}
    </section>
  );
}

function FormulaLibrary() {
  const formulas = useFactoryFormulas();
  const actions = useFactoryFormulaActions();
  const [definitionYaml, setDefinitionYaml] = useState('');
  const [formulaId, setFormulaId] = useState('');
  const [name, setName] = useState('');
  const [previewGoal, setPreviewGoal] = useState('Example goal');
  const [previewProject, setPreviewProject] = useState('/example/project');
  const [validation, setValidation] = useState<{ valid: boolean; errors: string[] }>();
  const [editing, setEditing] = useState(false);
  const [selectedRevisions, setSelectedRevisions] = useState<Record<string, number>>({});
  const actionError = [actions.copy, actions.validate, actions.preview, actions.save, actions.archive, actions.remove]
    .find((action) => action.isError)?.error;

  async function copy(id: string, revision: number, sourceName: string) {
    const draft = await actions.copy.mutateAsync({ id, revision });
    setDefinitionYaml(draft.definitionYaml.replace(/^name:.*$/m, `name: ${sourceName} copy`));
    setName(`${sourceName} copy`);
    setFormulaId('');
    setValidation(undefined);
    setEditing(true);
  }

  async function validate() {
    const result = await actions.validate.mutateAsync(definitionYaml);
    setValidation(result);
  }

  return (
    <section className="factory-panel" aria-labelledby="factory-formulas-heading">
      <h3 id="factory-formulas-heading">Formula library</h3>
      {formulas.isLoading && <div role="status">Loading Formulas…</div>}
      {formulas.isError && <div role="alert">Could not load Formulas.</div>}
      {actionError && <div role="alert">{actionError instanceof Error ? actionError.message : 'Formula action failed.'}</div>}
      <ul className="factory-formula-list">
        {(formulas.data ?? []).map((formula) => (
          <li key={formula.id}>
            <span><strong>{formula.name}</strong> {formula.revisions.map((revision) => `r${revision.revision}`).join(', ')} · {formula.origin}{formula.archived ? ' · archived' : ''}</span>
            <label>
              Revision for {formula.name}
              <select value={selectedRevisions[formula.id] ?? formula.currentRevision} onChange={(event) => setSelectedRevisions({ ...selectedRevisions, [formula.id]: Number(event.target.value) })}>
                {formula.revisions.map((revision) => <option key={revision.revision} value={revision.revision}>r{revision.revision}</option>)}
              </select>
            </label>
            <Button type="button" variant="muted" aria-label={`Copy ${formula.name} revision`} onClick={() => void copy(formula.id, selectedRevisions[formula.id] ?? formula.currentRevision, formula.name)}>Copy</Button>
            {formula.origin === 'custom' && !formula.archived && <Button type="button" variant="muted" onClick={() => void actions.archive.mutateAsync(formula.id)}>Archive</Button>}
            {formula.origin === 'custom' && <Button type="button" variant="muted" onClick={() => void actions.remove.mutateAsync(formula.id)}>Delete</Button>}
          </li>
        ))}
      </ul>
      {editing && (
        <form className="factory-formula-editor" onSubmit={(event) => event.preventDefault()} aria-label="Formula editor">
          <label>Custom Formula ID<input value={formulaId} onChange={(event) => setFormulaId(event.target.value)} placeholder="custom/team-delivery" /></label>
          <label>Formula name<input value={name} onChange={(event) => setName(event.target.value)} /></label>
          <label>Formula v1 YAML<textarea aria-label="Formula v1 YAML" value={definitionYaml} onChange={(event) => { setDefinitionYaml(event.target.value); setValidation(undefined); }} /></label>
          <div className="factory-formula-actions">
            <Button type="button" variant="muted" onClick={() => void validate()}>Validate</Button>
            <Button type="button" variant="muted" disabled={!validation?.valid} onClick={() => void actions.preview.mutateAsync({ definitionYaml, parameters: { goal: previewGoal, initial_project: previewProject } })}>Preview</Button>
            <Button type="button" variant="accent" disabled={!validation?.valid || !formulaId || !name} onClick={() => void actions.save.mutateAsync({ id: formulaId, name, definitionYaml }).then(() => setValidation(undefined))}>Save revision</Button>
          </div>
          <label>Preview goal<input value={previewGoal} onChange={(event) => setPreviewGoal(event.target.value)} /></label>
          <label>Preview project<input value={previewProject} onChange={(event) => setPreviewProject(event.target.value)} /></label>
          {validation && <div role={validation.valid ? 'status' : 'alert'}>{validation.valid ? 'Formula is valid.' : validation.errors.join(' ')}</div>}
          {actions.save.data && <div role="status">Saved revision {actions.save.data.revision}.</div>}
          {actions.preview.data && (
            <div className="factory-formula-preview" aria-label="Formula preview">
              <strong>{actions.preview.data.name}</strong>
              <ol>{actions.preview.data.nodes.map((node) => <li key={node.key}>{node.title} · {node.kind}</li>)}</ol>
              <ul>{actions.preview.data.edges.map((edge) => <li key={`${edge.from}-${edge.to}`}>{edge.from} {edge.type} {edge.to}</li>)}</ul>
            </div>
          )}
        </form>
      )}
    </section>
  );
}

function WorkEpics() {
  const epics = useWorkEpics();
  return (
    <section className="factory-panel" aria-labelledby="factory-epics-heading">
      <h3 id="factory-epics-heading">Work Epics</h3>
      {epics.isError && epics.data && (
        <div className="oc-error-banner" role="alert">
          Work Epics may be stale: {epics.error instanceof Error ? epics.error.message : 'refresh failed'}
          <button type="button" onClick={() => void epics.refetch()}>Retry Work Epics</button>
        </div>
      )}
      {epics.isLoading && !epics.data && <div role="status">Loading Work Epics…</div>}
      {epics.isError && !epics.data && (
        <div className="oc-error-banner" role="alert">
          {epics.error instanceof Error ? epics.error.message : 'Work Epics are unavailable.'}
          <button type="button" onClick={() => void epics.refetch()}>Retry Work Epics</button>
        </div>
      )}
      {epics.data?.length === 0 && (
        <div className="oc-empty">
          <strong>No Work Epics yet</strong>
          <p>Create one from the shipped Formula to start planning.</p>
        </div>
      )}
      {epics.data && epics.data.length > 0 && (
        <ul className="factory-epic-list">
          {epics.data.map((epic) => (
            <li key={epic.id}>
              <article aria-labelledby={`factory-epic-${epic.id}`}>
                <h4 id={`factory-epic-${epic.id}`}>{epic.goal}</h4>
                <dl>
                  <div><dt>Epic status</dt><dd>{epic.status}</dd></div>
                  <div><dt>Initial project</dt><dd>{epic.initialProject}</dd></div>
                  <div><dt>Planning Work status</dt><dd>{epic.planning.workStatus}</dd></div>
                  <div><dt>Plan approval Gate status</dt><dd>{epic.planning.approvalStatus}</dd></div>
                </dl>
				{epic.planError ? <div role="alert">{epic.planError}</div> : epic.plan && <PlanPanel key={`${epic.id}-${epic.plan.revision}`} epicID={epic.id} plan={epic.plan} />}
              </article>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

const dispatchColumns: Array<{ state: FactoryDispatchState; label: string }> = [
  { state: 'ready', label: 'Ready' },
  { state: 'running', label: 'Running' },
  { state: 'completed', label: 'Completed' },
];

function DispatchBoard({ items = [] }: { items?: FactoryDispatchItem[] }) {
  return (
    <section className="factory-dispatch" aria-labelledby="factory-dispatch-board-heading">
      <h4 id="factory-dispatch-board-heading">Dispatch board</h4>
      <div className="factory-dispatch-board">
        {dispatchColumns.map(({ state, label }) => {
          const columnItems = items.filter((item) => item.state === state);
          return (
            <section key={state} className={`factory-dispatch-column factory-dispatch-column-${state}`} aria-labelledby={`factory-dispatch-${state}`}>
              <h5 id={`factory-dispatch-${state}`}>{label} <span>{columnItems.length}</span></h5>
              {columnItems.length === 0 ? (
                <p className="factory-dispatch-empty">No {state} work.</p>
              ) : (
                <ul aria-label={`${label} dispatch items`}>
                  {columnItems.map((item) => (
                    <li key={item.id}>
                      <article aria-label={item.title}>
                        <strong>{item.title}</strong>
                        <span>{item.repository}</span>
                        <dl>
                          <div><dt>Epic</dt><dd>{item.epicId}</dd></div>
                          {item.attemptId && <div><dt>Attempt</dt><dd>{item.attemptId}</dd></div>}
                          {item.outcome && <div><dt>Outcome</dt><dd>{item.outcome}</dd></div>}
                        </dl>
                      </article>
                    </li>
                  ))}
                </ul>
              )}
            </section>
          );
        })}
      </div>
    </section>
  );
}

function ApprovedPlanRecord({ approval }: { approval: FactoryPlan['approval'] }) {
	if (!approval) return null;
	return <section aria-label="Approved Plan record">
		<h5>Approved Plan record</h5>
		<dl>
			<div><dt>Exact revision</dt><dd>revision {approval.revision}</dd></div>
			<div><dt>Exact hash</dt><dd><code>{approval.hash}</code></dd></div>
			<div><dt>Formula</dt><dd>{approval.formulaId} r{approval.formulaVersion} · {approval.formulaOrigin} · <code>{approval.formulaHash}</code> · instantiation {approval.instantiationId}</dd></div>
			<div><dt>Actor</dt><dd>{approval.actor}</dd></div>
			<div><dt>Approved at</dt><dd>{approval.approvedAt}</dd></div>
			<div><dt>Reason</dt><dd>{approval.reason || 'No reason recorded'}</dd></div>
		</dl>
		<pre>{JSON.stringify(approval.graph, null, 2)}</pre>
	</section>;
}

function PlanPanel({ epicID, plan }: { epicID: string; plan: FactoryPlan }) {
	const mutate = useMutateFactoryPlan(epicID);
	const addPlanning = useAddFactoryPlanningWork(epicID);
	const completePlanning = useCompleteFactoryPlanningWork(epicID);
	const decide = useDecideFactoryPlan(epicID);
	const [graphText, setGraphText] = useState(() => JSON.stringify(plan.graph, null, 2));
	const [graphError, setGraphError] = useState('');
	const [approvalAcknowledged, setApprovalAcknowledged] = useState(false);
	const [decisionReason, setDecisionReason] = useState('');
	const busy = mutate.isPending || addPlanning.isPending || completePlanning.isPending || decide.isPending;

	async function saveGraph() {
		try {
			const graph = JSON.parse(graphText) as FactoryPlanGraph;
			setGraphError('');
			await mutate.mutateAsync({ expectedRevision: plan.revision, graph });
		} catch (error) {
			setGraphError(error instanceof Error ? error.message : 'Invalid Plan graph.');
		}
	}

	async function addRepository(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		const form = new FormData(event.currentTarget);
		await addPlanning.mutateAsync({
			expectedRevision: plan.revision,
			target: {
				id: String(form.get('targetId') ?? '').trim(),
				hostId: 'local',
				repository: String(form.get('repository') ?? '').trim(),
				deliveryBase: { remote: String(form.get('remote') ?? '').trim(), baseBranch: String(form.get('baseBranch') ?? '').trim(), baseSha: String(form.get('baseSha') ?? '').trim() },
			},
		});
	}

	function decision(action: 'approve' | 'revise' | 'reject' | 'cancel') {
		void decide.mutateAsync({ action, request: { expectedRevision: plan.revision, expectedHash: plan.hash, actor: 'operator', reason: decisionReason.trim(), acknowledgeLocalExecution: action === 'approve' && approvalAcknowledged } });
	}

	return (
		<section className="factory-plan" aria-label={`Plan for ${epicID}`}>
			<p><strong>Plan {plan.state}</strong> · revision {plan.revision} · <code>{plan.hash.slice(0, 12)}</code></p>
			<ApprovedPlanRecord approval={plan.approval} />
			<ul aria-label="Planning Sessions">
				{plan.planning.map((work) => <li key={work.id}>
					{work.repository}: {work.status}{' '}
					{work.session.id && <a href={`/session/${encodeURIComponent(work.session.id)}?platform=${encodeURIComponent(work.session.platform)}`}>Open Planning Session</a>}{' '}
					{plan.state === 'draft' && (work.status !== 'closed' || work.completedRevision !== plan.revision || work.completedHash !== plan.hash) && <Button type="button" disabled={busy} onClick={() => void completePlanning.mutateAsync({ workID: work.id, expectedRevision: plan.revision, expectedHash: plan.hash })}>Mark Planning Work complete</Button>}
				</li>)}
			</ul>
			{plan.validation.length > 0 && <ul className="factory-plan-validation" aria-label="Plan validation">{plan.validation.map((problem) => <li key={problem}>{problem}</li>)}</ul>}
			{plan.state === 'draft' && (
				<>
					<label>Draft graph<textarea aria-label={`Draft graph for ${epicID}`} value={graphText} onChange={(event) => setGraphText(event.target.value)} /></label>
					<Button type="button" disabled={busy} onClick={() => void saveGraph()}>Save graph</Button>
					<form className="factory-target-form" aria-label={`Add Planning Work to ${epicID}`} onSubmit={(event) => void addRepository(event)}>
						<input name="targetId" aria-label="Target ID" placeholder="api" required />
						<input name="repository" aria-label="Target repository" placeholder="/absolute/repository" required />
						<input name="remote" aria-label="Delivery remote" placeholder="origin" required />
						<input name="baseBranch" aria-label="Delivery base branch" placeholder="main" required />
						<input name="baseSha" aria-label="Delivery base SHA" required />
						<label><input type="checkbox" required /> I understand this Planning Work executes locally without isolation.</label>
						<Button type="submit" disabled={busy}>Add Planning Work</Button>
					</form>
				</>
			)}
			<div className="factory-plan-actions">
				<label>Reason for Plan decision<input value={decisionReason} onChange={(event) => setDecisionReason(event.target.value)} /></label>
				{plan.state === 'draft' && plan.validation.length === 0 && <label><input type="checkbox" checked={approvalAcknowledged} onChange={(event) => setApprovalAcknowledged(event.target.checked)} /> I understand approved work executes locally without isolation under the displayed profiles.</label>}
				{plan.state === 'draft' && <Button type="button" variant="accent" disabled={busy || plan.validation.length > 0 || !approvalAcknowledged} onClick={() => decision('approve')}>Approve exact revision</Button>}
				{plan.state === 'approved' && <Button type="button" disabled={busy} onClick={() => decision('revise')}>Revise Plan</Button>}
				{plan.state === 'draft' && <Button type="button" disabled={busy} onClick={() => decision('reject')}>Reject Plan</Button>}
				{plan.state !== 'cancelled' && <Button type="button" disabled={busy} onClick={() => decision('cancel')}>Cancel Plan</Button>}
			</div>
			{(graphError || mutate.isError || addPlanning.isError || completePlanning.isError || decide.isError) && <div role="alert">{graphError || 'Plan mutation failed; refresh to reconcile the current revision.'}</div>}
		</section>
	);
}

export function MissionControl() {
  const status = useFactoryStatus();
  const healthy = status.data?.health === 'healthy';

  if (status.isLoading && !status.data) {
    return <div className="oc-list-loading" role="status"><span className="oc-spinner" />Loading Factory status…</div>;
  }
  if (status.isError && !status.data) {
    const message = status.error instanceof Error ? status.error.message : 'Factory status is unavailable.';
    return (
      <div className="oc-error-banner" role="alert">
        {message}
        <button type="button" onClick={() => void status.refetch()}>Retry</button>
      </div>
    );
  }
  if (!status.data) return null;

  const factory = status.data;
  return (
    <main className="factory-page">
      {status.isError && (
        <div className="oc-error-banner" role="alert">
          Factory status may be stale: {status.error instanceof Error ? status.error.message : 'refresh failed'}
          <button type="button" onClick={() => void status.refetch()}>Retry</button>
        </div>
      )}
      <div className="factory-heading">
        <div>
          <div className="factory-kicker">Software Factory</div>
          <h2>Mission Control</h2>
        </div>
        <div className={`factory-health factory-health-${factory.health}`} role={healthy ? 'status' : 'alert'}>
          <strong>{healthy ? `Healthy · ${factory.idle ? 'idle' : 'active'}` : factory.health}</strong>
          <span>{factory.message}</span>
        </div>
      </div>

      <section className="factory-panel" aria-labelledby="factory-dispatch-heading">
        <h3 id="factory-dispatch-heading">Factory dispatcher</h3>
        <p>
          {factory.dispatchOwner
            ? 'This process owns dispatch.'
            : 'Another local process owns dispatch; this process is read-only.'}
        </p>
        {factory.beads.version && (
          <small>Beads {factory.beads.version} · JSON contract {factory.beads.contractVersion}</small>
        )}
        <DispatchBoard items={factory.dispatch} />
      </section>

      {healthy && factory.dispatchOwner && <CreateWorkEpic />}
      {healthy && factory.dispatchOwner && <FormulaLibrary />}
      {healthy && <WorkEpics />}
    </main>
  );
}
