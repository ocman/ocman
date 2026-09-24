import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api, postJSON, type FactoryEpic } from '../lib/api';

export interface FactoryEpicModels { plan?: string; implementation?: string; verification?: string }
export type FactoryEpicWithModels = FactoryEpic & { models?: FactoryEpicModels };

export const epicModelPhases = [
	['plan', 'Planning model'],
	['implementation', 'Implementation model'],
	['verification', 'Verification model'],
] as const;

export function useFactoryEpicModels(epic?: FactoryEpicWithModels) {
	const client = useQueryClient();
	// ponytail: the reviewer picker's catalog lists every model OpenCode knows; no per-session context needed.
	const catalog = useQuery({ queryKey: ['factory-epic-model-options'], queryFn: ({ signal }) => api.getJudgeModelOptions(signal), staleTime: 60_000, retry: false, enabled: !!epic });
	const save = useMutation({
		mutationFn: (models: FactoryEpicModels) => postJSON<FactoryEpicModels, FactoryEpicModels>(`/api/factory/epics/${encodeURIComponent(epic!.id)}/models`, models),
		onSuccess: () => client.invalidateQueries({ queryKey: ['factory-epics'] }),
	});
	const models = epic?.models ?? {};
	return {
		models,
		options: catalog.data?.models ?? [],
		loading: catalog.isFetching,
		save,
		setModel: (phase: keyof FactoryEpicModels, value: string) => save.mutate({ ...models, [phase]: value }),
	};
}
