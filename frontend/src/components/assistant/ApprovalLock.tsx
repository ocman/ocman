import { useId, useRef, useState } from 'react';
import { useIsPrinting } from '../../lib/useIsPrinting';
import { usePrintCollapse } from '../../lib/printCollapseContext';
import type { ToolApproval } from '../../lib/threadHelpers';
import { Button } from '../Control';
import { Tooltip } from '../Tooltip';
import { Popover } from '../Popover';

function approvalLabel(approvals: ToolApproval[]): string {
  const byUser = approvals.some((approval) => approval.approvedBy === 'user');
  const byAI = approvals.some((approval) => approval.approvedBy === 'ai');
  return byUser && byAI ? 'Approved by user and AI' : byUser ? 'Approved by user' : 'Approved by AI';
}

function ApprovalDetail({ approvals }: { approvals: ToolApproval[] }) {
  return (
    <div className="oc-ai-approval-detail" data-testid="ai-approval-detail">
      {approvals.map((approval, i) => (
        <div className="oc-ai-approval-entry" key={i}>
          <div className="oc-ai-approval-by">{approval.approvedBy === 'user' ? 'Approved by user' : 'Approved by AI'}</div>
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
  );
}

/**
 * Permission approval shown as a lock beside the tool call it unblocked.
 * Hovering or focusing the lock reveals who approved and, for AI
 * approvals, the judge's reasoning. Printing shows the detail inline.
 */
export function ApprovalLock({ approvals }: { approvals: ToolApproval[] }) {
  const isPrinting = useIsPrinting();
  const printCollapse = usePrintCollapse();
  const [open, setOpen] = useState(false);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const id = useId();
  const [position, setPosition] = useState({ top: 0, left: 0, maxHeight: 0 });
  const label = approvalLabel(approvals);
  if (isPrinting && !printCollapse) return <ApprovalDetail approvals={approvals} />;
  return (
    <>
      <Tooltip content={<ApprovalDetail approvals={approvals} />}>
        <Button ref={triggerRef} variant="ghost" size="compact" type="button" className="oc-approval-lock" aria-label={label}
          aria-haspopup="dialog" aria-expanded={open} aria-controls={open ? id : undefined}
          onClick={(event) => {
            event.stopPropagation();
            const rect = event.currentTarget.getBoundingClientRect();
            const top = Math.max(8, Math.min(rect.bottom + 6, window.innerHeight - 160));
            setPosition({ top, left: Math.max(8, Math.min(rect.left, window.innerWidth - 320)), maxHeight: window.innerHeight - top - 8 });
            setOpen(!open);
          }}>
          <i className="bi bi-lock" aria-hidden="true" />
        </Button>
      </Tooltip>
      <Popover open={open} onClose={() => setOpen(false)} triggerRef={triggerRef} id={id} label="Permission approval details"
        className="oc-approval-popover" style={position}>
        <ApprovalDetail approvals={approvals} />
      </Popover>
    </>
  );
}
