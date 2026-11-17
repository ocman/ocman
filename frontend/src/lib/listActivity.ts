import type { Project, Session } from './api';
import { formatCount, formatDuration, formatNumber } from './format';

/** Effective cost includes estimates for unpriced models when available. */
export const sessionCost = (s: Pick<Session, 'totalCost' | 'totalEffectiveCost'>) => s.totalEffectiveCost || s.totalCost || 0;

export function formatCost(value: number): string {
  if (!(value > 0)) return '$0';
  if (value < 0.01) return '<$0.01';
  return value >= 1000 ? `$${formatNumber(value)}` : `$${value.toFixed(2)}`;
}

/** Plain-language definitions behind the compact list numbers. */
export const ACTIVITY_DEFINITIONS = {
  prompts: 'Prompts: messages you sent in the session',
  duration: 'Duration: first to last message, including idle time',
  cost: 'Cost: model spend, estimated where the provider reports none',
  messages: 'Messages: every user and agent message, including helper sessions',
  tokens: 'Tokens: fresh input / output; cache reads are excluded',
};

export function sessionActivityDetails(s: Session): string {
  return [
    formatCount(s.messageCount, 'prompt'),
    `${formatDuration(s.durationMs)} elapsed${s.activeDurationMs ? `, ${formatDuration(s.activeDurationMs)} agent working` : ''}`,
    `${formatCost(sessionCost(s))} cost`,
    `${formatNumber(s.totalInputTokens ?? 0)} input / ${formatNumber(s.totalOutputTokens ?? 0)} output tokens (excl. cache reads)`,
  ].join('\n');
}

export function projectActivityDetails(p: Project): string {
  return [
    `${formatCount(p.messageCount, 'message')} (user, agent and helper sessions)`,
    `${formatCost(p.totalCost ?? 0)} cost`,
    `${formatNumber(p.totalTokensIn)} input / ${formatNumber(p.totalTokensOut)} output tokens (excl. cache reads)`,
  ].join('\n');
}
