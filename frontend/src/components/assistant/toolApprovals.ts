import type { ToolApproval } from '../../lib/threadHelpers';

/** Only trusted, validated approval artifacts can add an approval lock. */
export function toolApprovals(artifact: unknown): ToolApproval[] {
  if (!artifact || typeof artifact !== 'object' || Array.isArray(artifact)) return [];
  const approvals = (artifact as { ocmanApprovals?: unknown }).ocmanApprovals;
  if (!Array.isArray(approvals)) return [];
  return approvals.filter((approval): approval is ToolApproval => {
    if (!approval || typeof approval !== 'object' || Array.isArray(approval)) return false;
    const value = approval as Partial<ToolApproval>;
    return typeof value.permission === 'string'
      && Array.isArray(value.patterns)
      && value.patterns.every((pattern) => typeof pattern === 'string')
      && typeof value.reasoning === 'string'
      && (value.approvedBy === 'user' || value.approvedBy === 'ai');
  });
}
