import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, type FactoryEpic } from '../lib/api';

export function implementationModelTier(model: string) {
	if (/fable|astra/i.test(model)) return 'Strong';
	if (/opus|(?:^|[-/])sol(?:$|[-/])/i.test(model)) return 'Balanced';
	if (/sonnet|terra/i.test(model)) return 'Fast';
	return 'Other';
}

export function useFactoryImplementationModel(epic?: FactoryEpic) {
	const { attempts = [], planGate, id } = epic ?? {};
	const session = attempts.findLast((attempt) => attempt.session.id)?.session;
	const catalog = useQuery({
		queryKey: ['factory-implementation-models', session?.platform, session?.id],
		queryFn: () => api.sessionModels(session!.id, session!.platform),
		enabled: !!session && planGate?.resolution === 'open',
		staleTime: 60_000,
		retry: false,
	});
	const models = (catalog.data?.models ?? []).filter((model) => model.isAvailable !== false).map((model) => `${model.provider}/${model.model}`);
	const suggested = models.find((model) => implementationModelTier(model) === 'Balanced') ?? models.find((model) => implementationModelTier(model) === 'Fast') ?? '';
	const [selection, setSelection] = useState<{ gate: string; model: string }>();
	const gate = `${id}/${planGate?.proposalHash}`;
	const model = planGate?.implementationModel ?? (selection?.gate === gate ? selection.model : suggested);
	return { model, models, setModel: (model: string) => setSelection({ gate, model }), loading: catalog.isFetching, error: catalog.isError, locked: planGate?.resolution === 'approved' };
}
