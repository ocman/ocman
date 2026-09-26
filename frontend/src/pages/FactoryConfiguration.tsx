import { useState, type FormEvent } from 'react';
import { EpicGraph } from './EpicGraph';
import { formulaIssues } from './factoryGraph';
import { Button, ButtonGroup, TextField, TextareaField } from '../components/Control';
import { useFactoryCapacityPolicy, useFactoryFormula, useFactoryFormulas, usePreviewFactoryFormula, useSaveFactoryFormula, useSetFactoryCapacityPolicy, useValidateFactoryFormula } from '../lib/queries';
import type { FactoryFormula } from '../lib/api';
import { TRACER_FORMULA_ID } from './factoryHelpers';
import { FactoryPage, QueryError } from './FactoryLayout';

function FormulaGraph({ formula }: { formula: FactoryFormula }) {
	return <>
		<EpicGraph key={formula.hash} issues={formulaIssues(formula)} preview />
		<details><summary>Compiled Formula JSON</summary><pre>{JSON.stringify(formula.compiled, null, 2)}</pre></details>
	</>;
}

export function FactoryConfiguration() {
	const formula = useFactoryFormula(TRACER_FORMULA_ID, 3);
	const formulas = useFactoryFormulas();
	const validateFormula = useValidateFactoryFormula();
	const previewFormula = usePreviewFactoryFormula();
	const saveFormula = useSaveFactoryFormula();
	const capacity = useFactoryCapacityPolicy();
	const saveCapacity = useSetFactoryCapacityPolicy();
	const [error, setError] = useState('');
	const [capacityError, setCapacityError] = useState('');
	const [formulaErrors, setFormulaErrors] = useState<string[]>([]);
	const [formulaSaved, setFormulaSaved] = useState('');
	const [selectedFormula, setSelectedFormula] = useState('new');
	const [editedSource, setFormulaSource] = useState<string>();
	const formulaSource = editedSource ?? formula.data?.source ?? '';
	const [formulaID, setFormulaID] = useState('');
	const policy = capacity.data;
	const inspectedFormula = formulas.data?.find((item) => `${item.id}@${item.version}` === selectedFormula);
	function editFormula(selected?: FactoryFormula) {
		setFormulaID(selected?.id === TRACER_FORMULA_ID ? 'custom/tracer' : selected?.id ?? '');
		setFormulaSource(selected?.source ?? formula.data?.source ?? '');
		setFormulaErrors([]);
		setFormulaSaved('');
		setError('');
		previewFormula.reset();
		validateFormula.reset();
	}
	async function save(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		const form = new FormData(event.currentTarget);
		try {
			const projectOverrides = JSON.parse(String(form.get('projectOverrides'))) as Record<string, number>;
			if (!projectOverrides || Array.isArray(projectOverrides)) throw new Error('Project capacity overrides must be a JSON object.');
			await saveCapacity.mutateAsync({ globalCapacity: Number(form.get('globalCapacity')), projectCapacity: Number(form.get('projectCapacity')), projectOverrides });
			setCapacityError('');
		} catch (reason) { setCapacityError(reason instanceof Error ? reason.message : 'Could not save capacity policy.'); }
	}
	async function saveFormulaRevision(formElement: HTMLFormElement, action: 'validate' | 'preview' | 'save') {
		if (!formElement.reportValidity()) return;
		const form = new FormData(formElement); const source = String(form.get('source')); const id = String(form.get('id')).trim();
		try {
			if (action === 'validate') setFormulaErrors((await validateFormula.mutateAsync({ id, source })).errors ?? [])
			else if (action === 'preview') setFormulaErrors((await previewFormula.mutateAsync({ id, source })).errors ?? [])
			else { const saved = await saveFormula.mutateAsync({ id, source }); setFormulaErrors([]); setFormulaSaved(`Formula saved: ${saved.id}@${saved.version}`) }
			setError('');
		} catch (reason) { setError(reason instanceof Error ? reason.message : 'Formula is invalid.'); }
	}
	return <FactoryPage>
		<h2>Factory configuration</h2>
		{capacity.isLoading && <p role="status">Loading capacity policy…</p>}
		{capacity.isError && <QueryError error={capacity.error} retry={() => void capacity.refetch()} />}
		{policy && <form onSubmit={(event) => void save(event)}>
			<label>Global implementation capacity<TextField aria-label="Global implementation capacity" name="globalCapacity" type="number" min="1" max="1000" required defaultValue={policy.globalCapacity} /></label>
			<label>Default project implementation capacity<TextField aria-label="Default project implementation capacity" name="projectCapacity" type="number" min="1" max="1000" required defaultValue={policy.projectCapacity} /></label>
			<label>Project capacity overrides (JSON)<TextareaField aria-label="Project capacity overrides (JSON)" name="projectOverrides" defaultValue={JSON.stringify(policy.projectOverrides, null, 2)} /></label>
			<Button type="submit" variant="accent" aria-busy={saveCapacity.isPending} disabled={saveCapacity.isPending}>{saveCapacity.isPending ? 'Saving…' : 'Save capacity policy'}</Button>
		</form>}
		{capacityError && <p role="alert">{capacityError}</p>}
		{error && <p role="alert">{error}</p>}
		{formula.isLoading && <p role="status">Loading Formula…</p>}
		{formula.isError && <QueryError error={formula.error} retry={() => void formula.refetch()} />}
		{formula.data && <section>
			<h3>{formula.data.name} · {formula.data.id}@{formula.data.version}</h3>
			<p>Content hash: {formula.data.hash}</p>
			<p>Source hash: {formula.data.sourceHash}</p>
			<p role="status">Formula is {formula.data.valid ? 'valid' : 'invalid'}</p>
			<details className="factory-formula-source"><summary>Tracer Formula source</summary><label>Tracer Formula source<TextareaField aria-label="Tracer Formula source" rows={15} readOnly value={formula.data.source} /></label></details>
			<Button type="button" onClick={() => { setSelectedFormula('new'); editFormula(formula.data); }}>Customize Tracer</Button>
			<h4>Graph</h4>
			<p>Inputs: {formula.data.inputs.join(', ')}</p>
			<FormulaGraph formula={formula.data} />
		</section>}
		<section>
			<h3>Custom Formula revisions</h3>
			{formulas.isError && <QueryError error={formulas.error} retry={() => void formulas.refetch()} />}
			<label>Formula<select aria-label="Formula" value={selectedFormula} onChange={(event) => { setSelectedFormula(event.target.value); editFormula(formulas.data?.find((item) => `${item.id}@${item.version}` === event.target.value)); }}><option value="new">New Formula</option>{formulas.data?.filter((item) => item.id !== TRACER_FORMULA_ID).map((item) => <option key={`${item.id}@${item.version}`} value={`${item.id}@${item.version}`}>{item.name} · {item.id}@{item.version}</option>)}</select></label>
			{inspectedFormula && <section aria-label="Formula inspection"><p>Content hash: {inspectedFormula.hash}</p><p>Source hash: {inspectedFormula.sourceHash}</p><FormulaGraph formula={inspectedFormula} /><details className="factory-formula-source"><summary>Stored Formula source</summary><label>Stored Formula source<TextareaField aria-label="Stored Formula source" rows={15} readOnly value={inspectedFormula.source} /></label></details></section>}
			<form onSubmit={(event) => { event.preventDefault(); void saveFormulaRevision(event.currentTarget, 'save'); }}>
				<label>Custom Formula ID<TextField aria-label="Custom Formula ID" name="id" required pattern="custom/[a-z][a-z0-9_-]*" value={formulaID} onChange={(event) => setFormulaID(event.target.value)} /></label>
				<p>Define named steps with kind, needs, prompt, and config in the YAML source. Implementation contains the planned tasks; downstream checks wait for all required tasks. Saving creates a new revision.</p>
				<details className="factory-formula-source"><summary>Formula source</summary><label>Formula YAML<TextareaField aria-label="Formula YAML" name="source" rows={15} required value={formulaSource} onChange={(event) => setFormulaSource(event.target.value)} onInvalid={(event) => { event.currentTarget.closest('details')!.open = true; }} /></label></details>
				<ButtonGroup label="Formula actions">
					<Button type="button" aria-busy={validateFormula.isPending} onClick={(event) => { if (event.currentTarget.form) void saveFormulaRevision(event.currentTarget.form, 'validate'); }} disabled={validateFormula.isPending || previewFormula.isPending || saveFormula.isPending}>{validateFormula.isPending ? 'Validating…' : 'Validate Formula'}</Button>
					<Button type="button" aria-busy={previewFormula.isPending} onClick={(event) => { if (event.currentTarget.form) void saveFormulaRevision(event.currentTarget.form, 'preview'); }} disabled={validateFormula.isPending || previewFormula.isPending || saveFormula.isPending}>{previewFormula.isPending ? 'Previewing…' : 'Preview Formula'}</Button>
					<Button type="submit" variant="accent" aria-busy={saveFormula.isPending} disabled={validateFormula.isPending || previewFormula.isPending || saveFormula.isPending}>{saveFormula.isPending ? 'Saving…' : 'Save immutable revision'}</Button>
				</ButtonGroup>
			</form>
			{validateFormula.data && <p role="status">Formula is {validateFormula.data.valid ? 'valid' : 'invalid'}{validateFormula.data.valid && `: ${validateFormula.data.hash}`}</p>}
			{previewFormula.data?.valid && <section aria-label="Formula preview"><FormulaGraph formula={previewFormula.data} /></section>}
			{previewFormula.data && <p role="status">Preview {previewFormula.data.valid ? `valid: ${previewFormula.data.hash}` : 'invalid'}</p>}
			{!!formulaErrors.length && <section role="alert" aria-label="Formula diagnostics"><p>Formula diagnostics</p><ul>{formulaErrors.map((diagnostic, index) => <li key={`${diagnostic}-${index}`}>{diagnostic}</li>)}</ul></section>}
			{formulaSaved && <p role="status">{formulaSaved}</p>}
		</section>
	</FactoryPage>;
}
