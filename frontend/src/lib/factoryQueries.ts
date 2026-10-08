import { useMutation, useQuery, useQueryClient, useQueries, type QueryClient } from '@tanstack/react-query';
import { api } from './api';
import type {
  FactoryEpic, CreateWorkEpicRequest, FactoryIssue, FactoryIssueComment,
  FactoryQueueItem, FactoryProposal, FactoryFormula, FactoryCapacityPolicy,
  FactoryPlanGateDecisionRequest, FactoryGraphMutation,
} from './api';

const factoryRefetchInterval = 15_000;

export function useWorkEpics(enabled = true) {
  return useQuery<FactoryEpic[]>({
    queryKey: ['factory-epics'],
    queryFn: ({ signal }) => api.factoryEpics(signal),
    enabled,
    refetchInterval: factoryRefetchInterval,
  });
}

export function useWorkEpic(id: string) {
  return useQuery<FactoryEpic>({
    queryKey: ['factory-epics', id],
    queryFn: ({ signal }) => api.factoryEpic(id, signal),
    enabled: Boolean(id),
    refetchInterval: factoryRefetchInterval,
  });
}

export function useFactoryIssues(id: string) {
  return useQuery<FactoryIssue[]>({
    queryKey: ['factory-epics', id, 'issues'],
    queryFn: ({ signal }) => api.factoryIssues(id, signal),
    enabled: Boolean(id),
    refetchInterval: factoryRefetchInterval,
  });
}

export function useFactoryIssueComments(epicID: string, issueID: string) {
  return useQuery<FactoryIssueComment[]>({
    queryKey: ['factory-issue-comments', epicID, issueID],
    queryFn: ({ signal }) => api.factoryIssueComments(epicID, issueID, signal),
    enabled: Boolean(epicID && issueID),
    refetchInterval: factoryRefetchInterval,
  });
}

export function useAddFactoryIssueComment(epicID: string, issueID: string) {
  const client = useQueryClient();
  return useMutation({
    mutationFn: (body: string) => api.addFactoryIssueComment(epicID, issueID, body),
    onSuccess: (comment) => client.setQueryData<FactoryIssueComment[]>(['factory-issue-comments', epicID, issueID], (comments = []) => [...comments, comment]),
  });
}

export function useFactoryRemovedIssues(id: string) {
  return useQuery<FactoryIssue[]>({
    queryKey: ['factory-epics', id, 'removed-issues'],
    queryFn: ({ signal }) => api.factoryRemovedIssues(id, signal),
    enabled: Boolean(id),
  });
}

export function useFactoryGraphIssues(epics: FactoryEpic[] | undefined, enabled = true) {
  return useQueries({ queries: (epics ?? []).map((epic) => ({ queryKey: ['factory-epics', epic.id, 'issues'], queryFn: ({ signal }: { signal: AbortSignal }) => api.factoryIssues(epic.id, signal), enabled, refetchInterval: factoryRefetchInterval })) });
}

export function useMutateFactoryGraph(id: string) {
  const client = useQueryClient();
  return useMutation({ mutationFn: (mutation: FactoryGraphMutation) => api.mutateFactoryGraph(id, mutation), onSuccess: () => client.invalidateQueries({ queryKey: ['factory-epics'] }) });
}

export function useFactoryQueue() {
  return useQuery<FactoryQueueItem[]>({
    queryKey: ['factory-queue'],
    queryFn: ({ signal }) => api.factoryQueue(signal),
    refetchInterval: factoryRefetchInterval,
  });
}

export function useResolveFactoryRecoveryGate() {
  const client = useQueryClient();
  return useMutation({
    mutationFn: ({ id, action, response }: { id: string; action: 'resume' | 'retry' | 'cancel'; response: string }) => api.resolveFactoryRecoveryGate(id, action, response),
    onSettled: () => invalidateFactoryState(client),
  });
}

export function useResolveFactoryAuthorityGate() {
  const client = useQueryClient();
  return useMutation({ mutationFn: ({ id, action }: { id: string; action: 'approve' | 'reject' }) => api.resolveFactoryAuthorityGate(id, action), onSuccess: () => invalidateFactoryState(client) });
}

export function useResolveFactoryProjectGate() {
  const client = useQueryClient();
  return useMutation({ mutationFn: ({ id, action, response, acknowledge }: { id: string; action: 'approve' | 'reject'; response: string; acknowledge: boolean }) => api.resolveFactoryProjectGate(id, action, response, acknowledge), onSettled: () => invalidateFactoryState(client) });
}

export function useFactoryProposals(id: string) {
  return useQuery<FactoryProposal[]>({
    queryKey: ['factory-epics', id, 'proposals'],
    queryFn: ({ signal }) => api.factoryProposals(id, signal),
    enabled: Boolean(id),
    refetchInterval: factoryRefetchInterval,
  });
}

export function useDecideFactoryPlanGate(id: string) {
  const client = useQueryClient();
  return useMutation({ mutationFn: ({ action, ...request }: FactoryPlanGateDecisionRequest & { action: 'approve' | 'revise' | 'reject' }) => api.factoryPlanGate(id, action, request), onSuccess: () => invalidateFactoryState(client) });
}
export function useCloseFactoryMol(id: string) {
  const client = useQueryClient();
  return useMutation({ mutationFn: (molID: string) => api.factoryCloseMol(id, molID), onSuccess: () => client.invalidateQueries({ queryKey: ['factory-epics', id] }) });
}
export function useCloseFactoryEpic(id: string) {
  const client = useQueryClient();
  return useMutation({ mutationFn: (force: boolean) => api.factoryCloseEpic(id, force), onSuccess: () => invalidateFactoryState(client) });
}
export function useSetFactoryEpicPaused(id: string) {
  const client = useQueryClient();
  return useMutation({ mutationFn: (paused: boolean) => api.factorySetEpicPaused(id, paused), onSuccess: () => invalidateFactoryState(client) });
}

export function useFactoryFormula(id: string, version: number) {
  return useQuery<FactoryFormula>({
    queryKey: ['factory-formulas', id, version],
    queryFn: ({ signal }) => api.factoryFormula(id, version, signal),
    enabled: Boolean(id) && version > 0,
  });
}

export function useFactoryFormulas() { return useQuery<FactoryFormula[]>({ queryKey: ['factory-formulas'], queryFn: ({ signal }) => api.factoryFormulas(signal) }); }
export function useValidateFactoryFormula() { return useMutation({ mutationFn: api.validateFactoryFormula }); }
export function usePreviewFactoryFormula() { return useMutation({ mutationFn: api.previewFactoryFormula }); }
export function useSaveFactoryFormula() { const client = useQueryClient(); return useMutation({ mutationFn: api.saveFactoryFormula, onSuccess: () => client.invalidateQueries({ queryKey: ['factory-formulas'] }) }); }

export function useFactoryCapacityPolicy() {
  return useQuery<FactoryCapacityPolicy>({ queryKey: ['factory-capacity-policy'], queryFn: ({ signal }) => api.factoryCapacityPolicy(signal) });
}

export function useSetFactoryCapacityPolicy() {
  const queryClient = useQueryClient();
  return useMutation({ mutationFn: (policy: FactoryCapacityPolicy) => api.setFactoryCapacityPolicy(policy), onSuccess: (policy) => queryClient.setQueryData(['factory-capacity-policy'], policy) });
}

export function useCreateWorkEpic() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (request: CreateWorkEpicRequest) => api.createFactoryEpic(request),
    onSuccess: async (epic) => {
      queryClient.setQueryData<FactoryEpic[]>(['factory-epics'], (epics = []) => [
        epic,
        ...epics.filter((item) => item.id !== epic.id),
      ]);
      await queryClient.invalidateQueries({ queryKey: ['factory-epics'] });
    },
  });
}

function invalidateFactoryState(client: QueryClient) {
  return Promise.all([
    client.invalidateQueries({ queryKey: ['factory-epics'] }),
    client.invalidateQueries({ queryKey: ['factory-queue'] }),
  ]);
}

export function useClaimFactoryPlan(id: string) {
  const client = useQueryClient();
  return useMutation({ mutationFn: (issueID: string) => api.factoryClaimPlan(id, issueID), onSettled: () => client.invalidateQueries({ queryKey: ['factory-epics'] }) });
}

export function useMaterializeFactoryPlan() {
  const client = useQueryClient();
  return useMutation({ mutationFn: ({ epicId, issueId }: { epicId: string; issueId: string }) => api.factoryMaterialize(epicId, issueId), onSuccess: () => invalidateFactoryState(client) });
}

export function useReopenFactoryIssue() {
  const client = useQueryClient();
  return useMutation({ mutationFn: ({ epicId, issueId }: { epicId: string; issueId: string }) => api.reopenFactoryIssue(epicId, issueId), onSuccess: () => invalidateFactoryState(client) });
}

export function useInvestigateFactoryUnblock() {
  return useMutation({ mutationFn: ({ epicId, issueId }: { epicId: string; issueId: string }) => api.investigateFactoryUnblock(epicId, issueId) });
}
