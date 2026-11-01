export interface ProviderSpend {
  estimatedUSD: number | null;
  inputTokens: number;
  outputTokens: number;
  requests: number;
  unpricedRequests: number;
  periodStart: string;
  periodEnd: string;
}

export interface ProviderBudget {
  id?: string;
  name: string;
  description?: string;
  limitUSD: number;
  spentUSD: number;
  remainingUSD: number;
  resetsAt?: string;
}
