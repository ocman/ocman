import { useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { api, type FactoryEpic } from '../lib/api';
import type { FactoryEpicWithModels } from './useFactoryEpicModels';

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
	const entries = (catalog.data?.models ?? []).filter((model) => !catalog.data?.hasProviders || model.isAvailable !== false);
	const models = entries.map((model) => `${model.provider}/${model.model}`);
	const suggested = models.find((model) => implementationModelTier(model) === 'Balanced') ?? models.find((model) => implementationModelTier(model) === 'Fast') ?? '';
	const [selection, setSelection] = useState<{ gate: string; model: string }>();
	const gate = `${id}/${planGate?.proposalHash}`;
	const selected = selection?.gate === gate;
	const retained = planGate?.implementationModel ?? (epic?.proposal?.manifest?.baseRevision ? '' : suggested);
	const { model, locked } = resolveImplementationModel(epic, selected ? selection.model : retained);
	const approvalModel = model ? { implementationModel: model } : selected && !locked ? { useDefaultImplementationModel: true } : {};
	return { model, models, entries, approvalModel, setModel: (model: string) => setSelection({ gate, model }), loading: catalog.isFetching, error: catalog.isError, locked };
}

// An epic-level implementation model wins at claim time, so the approval picker just mirrors it.
function resolveImplementationModel(epic: FactoryEpicWithModels | undefined, picked: string) {
	const epicModel = epic?.models?.implementation;
	if (epicModel) return { model: epicModel, locked: true };
	const locked = epic?.planGate?.resolution === 'approved';
	return { model: locked ? epic?.planGate?.implementationModel ?? picked : picked, locked };
}
