import { useRef, useState, type FormEvent } from 'react';
import { Button } from '../components/Control';
import { useAddFactoryPlanningWork, useCompleteFactoryPlanningWork, useCreateWorkEpic, useDecideFactoryPlan, useFactoryStatus, useMutateFactoryPlan, useWorkEpics } from '../lib/queries';
import type { FactoryPlan, FactoryPlanGraph } from '../lib/api';
import './MissionControl.css';

function CreateWorkEpic() {
  const createEpic = useCreateWorkEpic();
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
    const key = JSON.stringify([goal, initialProject]);
    if (pendingInstantiation.current?.key !== key) {
      pendingInstantiation.current = { key, id: crypto.randomUUID() };
    }
    await createEpic.mutateAsync({
      instantiationId: pendingInstantiation.current.id,
      goal,
      initialProject,
      acknowledgeLocalExecution: true,
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
          <label>Shipped Formula<input value="ocman/default v1" readOnly /></label>
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

function PlanPanel({ epicID, plan }: { epicID: string; plan: FactoryPlan }) {
	const mutate = useMutateFactoryPlan(epicID);
	const addPlanning = useAddFactoryPlanningWork(epicID);
	const completePlanning = useCompleteFactoryPlanningWork(epicID);
	const decide = useDecideFactoryPlan(epicID);
	const [graphText, setGraphText] = useState(() => JSON.stringify(plan.graph, null, 2));
	const [graphError, setGraphError] = useState('');
	const [approvalAcknowledged, setApprovalAcknowledged] = useState(false);
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
		void decide.mutateAsync({ action, request: { expectedRevision: plan.revision, expectedHash: plan.hash, actor: 'operator', acknowledgeLocalExecution: action === 'approve' && approvalAcknowledged } });
	}

	return (
		<section className="factory-plan" aria-label={`Plan for ${epicID}`}>
			<p><strong>Plan {plan.state}</strong> · revision {plan.revision} · <code>{plan.hash.slice(0, 12)}</code></p>
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
      </section>

      {healthy && factory.dispatchOwner && <CreateWorkEpic />}
      {healthy && <WorkEpics />}
    </main>
  );
}
