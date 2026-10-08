export interface AgentMetrics {
  agent: string;
  requests: number;
  successfulRequests: number;
  errorRequests: number;
  errorRate: number;
  inputTokens: number;
  outputTokens: number;
  totalTokens: number;
  totalDurationMs: number;
  effectiveCost: number;
  /** Response time excluding the union of tool intervals within each request. */
  agentDurationMs: number;
  toolDurationMs: number;
  unknownDurationMs: number;
}
