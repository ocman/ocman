import { useState, useId, useRef } from 'react';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import type { ToolApproval } from '../../lib/threadHelpers';

/**
 * Permission approval shown under the tool call it unblocked. AI
 * approvals reveal the judge's reasoning when clicked.
 */
export function ApprovalFootnote({ approvals }: { approvals: ToolApproval[] }) {
  const [openState, setOpen] = useState(false);
  const detailId = useId();
  const triggerRef = useRef<HTMLButtonElement>(null);
  const isPrinting = useIsPrinting();
  const printCollapse = usePrintCollapse();
  const open = openState || (isPrinting && !printCollapse);
  const aiApprovals = approvals.filter((approval) => approval.approvedBy === 'ai');
  const userApproved = approvals.some((approval) => approval.approvedBy === 'user');
  return (
    <div className="oc-ai-approval-footnote">
      {userApproved && <div>Approved by user</div>}
      {aiApprovals.length > 0 && (
        <button
          ref={triggerRef}
          type="button"
          className="oc-ai-approval-toggle"
          aria-expanded={open}
          aria-controls={detailId}
          onClick={() => setOpen(!openState)}
          onKeyDown={(event) => {
            if (event.key !== 'Escape' || !openState) return;
            event.preventDefault();
            setOpen(false);
            triggerRef.current?.focus();
          }}
        >
          Approved by AI
        </button>
      )}
      {open && aiApprovals.length > 0 && (
        <div
          className="oc-ai-approval-detail"
          data-testid="ai-approval-detail"
          id={detailId}
          role="region"
          aria-label="AI approval reason"
        >
          {aiApprovals.map((approval, i) => (
            <div className="oc-ai-approval-entry" key={i}>
              {approval.permission && (
                <div className="oc-ai-approval-permission">{approval.permission}</div>
              )}
              {approval.patterns.length > 0 && (
                <div className="oc-ai-approval-patterns">{approval.patterns.join(', ')}</div>
              )}
              {approval.reasoning && (
                <div className="oc-ai-approval-reasoning">{approval.reasoning}</div>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
