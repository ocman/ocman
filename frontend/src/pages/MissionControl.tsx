import { useRef, useState, type FormEvent } from 'react';
import { Button } from '../components/Control';
import { useCreateWorkEpic, useFactoryFormulaActions, useFactoryFormulas, useFactoryStatus, useWorkEpics } from '../lib/queries';
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
                (formula.revisions.length ? formula.revisions : [{ revision: formula.currentRevision }]).map((revision) => (
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
              </article>
            </li>
          ))}
        </ul>
      )}
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
      {healthy && factory.dispatchOwner && <FormulaLibrary />}
      {healthy && <WorkEpics />}
    </main>
  );
}
