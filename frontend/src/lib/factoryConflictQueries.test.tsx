// @vitest-environment jsdom
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { act, cleanup, renderHook } from '@testing-library/react';
import type { ReactNode } from 'react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { WorkEpic } from './api';
import { useCompleteFactoryPlanningWork, useDecideFactoryPlan } from './queries';

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

function setup() {
	const client = new QueryClient({ defaultOptions: { mutations: { retry: false } } });
	const wrapper = ({ children }: { children: ReactNode }) => <QueryClientProvider client={client}>{children}</QueryClientProvider>;
	const epic = { id: 'epic-1', plan: { revision: 4 } } as WorkEpic;
	client.setQueryData(['factory-epics'], [epic]);
	client.setQueryData(['factory-epics', 'epic-1'], epic);
	return { client, wrapper };
}

function rejectWithCurrentPlan() {
	const plan = { revision: 5, hash: 'hash-5', state: 'draft' as const, graph: { intent: 'Current', targets: [], items: [], dependencies: [] }, planning: [], validation: [] };
	vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({ stale: true, plan }), { status: 409, headers: { 'Content-Type': 'application/json' } })));
	return plan;
}

describe('Factory conflict queries', () => {
	it.each(['approve', 'revise', 'reject', 'cancel'] as const)('reconciles an actual rejected %s response', async (action) => {
		const plan = rejectWithCurrentPlan();
		const { client, wrapper } = setup();
		const mutation = renderHook(() => useDecideFactoryPlan('epic-1'), { wrapper });

		await act(() => mutation.result.current.mutateAsync({ action, request: { expectedRevision: 4, expectedHash: 'hash-4', actor: 'operator' } }));

		expect(client.getQueryData<WorkEpic>(['factory-epics', 'epic-1'])?.plan).toEqual(plan);
		expect(client.getQueryData<WorkEpic[]>(['factory-epics'])?.[0].plan).toEqual(plan);
	});

	it('reconciles an actual rejected Planning Work completion response', async () => {
		const plan = rejectWithCurrentPlan();
		const { client, wrapper } = setup();
		const mutation = renderHook(() => useCompleteFactoryPlanningWork('epic-1'), { wrapper });

		await act(() => mutation.result.current.mutateAsync({ workID: 'work-1', expectedRevision: 4, expectedHash: 'hash-4' }));

		expect(client.getQueryData<WorkEpic>(['factory-epics', 'epic-1'])?.plan).toEqual(plan);
		expect(client.getQueryData<WorkEpic[]>(['factory-epics'])?.[0].plan).toEqual(plan);
	});
});
