import { ModelSelect } from './ModelSelect';
import { Button } from './Control';
import { epicModelPhases, useFactoryEpicModels, type FactoryEpicWithModels } from './useFactoryEpicModels';

export function FactoryEpicModels({ epic }: { epic: FactoryEpicWithModels }) {
	const { models, options, entries, loading, error, refresh, save, setModel } = useFactoryEpicModels(epic);
	return <fieldset className="factory-epic-models" aria-label="Epic models">
		<legend>Models</legend>
		{epicModelPhases.map(([phase, label]) => <label key={phase}>{label}<ModelSelect value={models[phase] ?? ''} models={options} modelEntries={entries} allowCustom onChange={(value) => setModel(phase, value)} ariaLabel={label} defaultLabel="Default" disabled={save.isPending || epic.status === 'closed'} /></label>)}
		<span>Changes apply only to work that has not started yet.</span>
		<span>Missing a configured model? Restart the project's OpenCode instance after changing its configuration, then refresh. You can also enter its provider/model ID in the search field.</span>
		<Button type="button" onClick={refresh} disabled={loading}>Refresh models</Button>
		{loading && <span role="status">Loading available models…</span>}
		{error && <span role="alert">Could not load models for some projects. You can still select a model by its provider/model ID.</span>}
		{save.isError && <span role="alert">{save.error instanceof Error ? save.error.message : 'Could not save models.'}</span>}
	</fieldset>;
}
