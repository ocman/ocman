import { useMutation, useQueries, useQueryClient } from '@tanstack/react-query';
import { api, postJSON, type FactoryEpic, type SessionModelEntry } from '../lib/api';
import { availabilityUnknown } from '../lib/modelCatalogCache';

export interface FactoryEpicModels { plan?: string; implementation?: string; verification?: string }
export type FactoryEpicWithModels = FactoryEpic & { models?: FactoryEpicModels };

export const epicModelPhases = [
	['plan', 'Planning model'],
	['implementation', 'Implementation model'],
	['verification', 'Verification model'],
] as const;

export function useFactoryEpicModels(epic?: FactoryEpicWithModels) {
	const client = useQueryClient();
	const projects = [...new Set([epic?.initialProject, ...(epic?.projects ?? []).map(({ path }) => path)].filter((path): path is string => !!path))];
	const catalogs = useQueries({ queries: projects.map((directory) => ({
		queryKey: ['factory-epic-model-options', 'local', directory],
		queryFn: ({ signal }: { signal: AbortSignal }) => api.prepareSession({ directory, remoteId: 'local' }, signal),
		staleTime: 60_000,
		retry: false,
	})) });
	// Availability is project-specific; this union is for selection, not a guarantee for every project.
	const allEntries = catalogs.flatMap((catalog) => availabilityUnknown(catalog.data?.models.models ?? []));
	const merged = new Map<string, SessionModelEntry>();
	for (const entry of allEntries) {
		const key = `${entry.provider}/${entry.model}`;
		const previous = merged.get(key);
		merged.set(key, { ...entry, modelName: entry.modelName || previous?.modelName, providerName: entry.providerName || previous?.providerName });
	}
	const entries = [...merged.values()];
	const save = useMutation({
		mutationFn: (models: FactoryEpicModels) => postJSON<FactoryEpicModels, FactoryEpicModels>(`/api/factory/epics/${encodeURIComponent(epic!.id)}/models`, models),
		onSuccess: () => client.invalidateQueries({ queryKey: ['factory-epics'] }),
	});
	const models = epic?.models ?? {};
	return {
		models,
		options: entries.map((entry) => `${entry.provider}/${entry.model}`),
		entries,
		loading: catalogs.some((catalog) => catalog.isFetching),
		error: catalogs.some((catalog) => catalog.isError),
		refresh: () => { for (const catalog of catalogs) void catalog.refetch(); },
		save,
		setModel: (phase: keyof FactoryEpicModels, value: string) => save.mutate({ ...models, [phase]: value }),
	};
}
