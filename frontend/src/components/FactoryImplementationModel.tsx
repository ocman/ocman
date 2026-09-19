import { implementationModelTier, type useFactoryImplementationModel } from './useFactoryImplementationModel';
import { SelectField } from './Control';

export function FactoryImplementationModel({ model, models, setModel, loading, error, locked }: ReturnType<typeof useFactoryImplementationModel>) {
	return <span>
		<label>Implementation model<SelectField value={model} onChange={(event) => setModel(event.target.value)} disabled={loading || locked}>
			<option value="">Runtime default</option>
			{model && !models.includes(model) && <option value={model}>{model}</option>}
			{models.map((value) => <option key={value} value={value}>{value} · {implementationModelTier(value)}</option>)}
		</SelectField></label>
		<span>Planning: Fable / Astra. Implementation: Opus / Sol recommended; Sonnet / Terra for speed.</span>
		{loading && <span role="status">Loading available models…</span>}
		{error && <span role="status">Could not load models. Runtime default is available.</span>}
	</span>;
}
