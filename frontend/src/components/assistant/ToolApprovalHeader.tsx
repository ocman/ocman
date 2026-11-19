import type { ReactNode } from 'react';
import type { ToolApproval } from '../../lib/threadHelpers';
import { ApprovalLock } from './ApprovalLock';

/** Keep the lock after the heading, outside any expand/collapse button. */
export function ToolApprovalHeader({ approvals, children }: { approvals: ToolApproval[]; children: ReactNode }) {
  if (!approvals.length) return children;
  return (
    <div className="oc-tool-with-approval">
      <div className="oc-tool-with-approval-body">{children}</div>
      <ApprovalLock approvals={approvals} />
    </div>
  );
}
