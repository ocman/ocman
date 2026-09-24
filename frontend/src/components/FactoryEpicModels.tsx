import { SelectField } from './Control';
import { implementationModelTier } from './useFactoryImplementationModel';
import { epicModelPhases, useFactoryEpicModels, type FactoryEpicWithModels } from './useFactoryEpicModels';

export function FactoryEpicModels({ epic }: { epic: FactoryEpicWithModels }) {
	const { models, options, loading, save, setModel } = useFactoryEpicModels(epic);
	return <fieldset className="factory-epic-models" aria-label="Epic models">
		<legend>Models</legend>
		{epicModelPhases.map(([phase, label]) => {
			const value = models[phase] ?? '';
			return <label key={phase}>{label}<SelectField value={value} disabled={save.isPending || epic.status === 'closed'} onChange={(event) => setModel(phase, event.target.value)}>
				<option value="">Default</option>
				{value && !options.includes(value) && <option value={value}>{value}</option>}
				{options.map((option) => <option key={option} value={option}>{option} · {implementationModelTier(option)}</option>)}
			</SelectField></label>;
		})}
		<span>Changes apply only to work that has not started yet.</span>
		{loading && <span role="status">Loading available models…</span>}
		{save.isError && <span role="alert">{save.error instanceof Error ? save.error.message : 'Could not save models.'}</span>}
	</fieldset>;
}
