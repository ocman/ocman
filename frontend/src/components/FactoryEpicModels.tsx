import { ModelSelect } from './ModelSelect';
import { epicModelPhases, useFactoryEpicModels, type FactoryEpicWithModels } from './useFactoryEpicModels';

export function FactoryEpicModels({ epic }: { epic: FactoryEpicWithModels }) {
	const { models, options, loading, save, setModel } = useFactoryEpicModels(epic);
	return <fieldset className="factory-epic-models" aria-label="Epic models">
		<legend>Models</legend>
		{epicModelPhases.map(([phase, label]) => <label key={phase}>{label}<ModelSelect value={models[phase] ?? ''} models={options} onChange={(value) => setModel(phase, value)} ariaLabel={label} defaultLabel="Default" disabled={save.isPending || epic.status === 'closed'} /></label>)}
		<span>Changes apply only to work that has not started yet.</span>
		{loading && <span role="status">Loading available models…</span>}
		{save.isError && <span role="alert">{save.error instanceof Error ? save.error.message : 'Could not save models.'}</span>}
	</fieldset>;
}
