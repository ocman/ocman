export interface ProviderSpend {
  estimatedUSD: number | null;
  inputTokens: number;
  outputTokens: number;
  requests: number;
  unpricedRequests: number;
  periodStart: string;
  periodEnd: string;
}
