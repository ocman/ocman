import { useState } from 'react';
import { createPortal } from 'react-dom';
import type { Loop } from '../lib/api.types';
import { useLoopsStore } from '../lib/loopsStore';
import { relativeTime, shortPath } from '../lib/format';
import { loopBudgetLabel, loopTriggerLabel, nextRunLabel } from '../lib/loopFormat';
import { LoopEditModal } from './LoopEditModal';
import { LoopHistoryModal } from './LoopHistoryModal';

export function LoopTableRow({ loop }: { loop: Loop }) {
  const resume = useLoopsStore((s) => s.resume);
  const pause = useLoopsStore((s) => s.pause);
  const trigger = useLoopsStore((s) => s.trigger);
  const remove = useLoopsStore((s) => s.remove);
  const update = useLoopsStore((s) => s.update);
  const restart = useLoopsStore((s) => s.restart);
  const [editing, setEditing] = useState(false);
  const [history, setHistory] = useState(false);

  const terminal = ['completed', 'deleted', 'errored'].includes(loop.state);
  // completed/errored loops can be revived; deleted ones can't.
  const restartable = loop.state === 'completed' || loop.state === 'errored';
  const next = nextRunLabel(loop);

  return (
    <tr className="oc-loops-tr" data-testid="loop-row" data-loop-state={loop.state}>
      <td className="oc-loops-td-title" title={loop.lastSummary || undefined}>
        <div className="oc-loops-title-main">{loop.title || loop.id}</div>
        <div className="mono oc-loops-title-sub">
          {loopTriggerLabel(loop)} &middot; {loop.actionType} &middot; <span data-testid="loop-budget">{loopBudgetLabel(loop)}</span>
        </div>
      </td>
      <td>
        <span className="oc-loop-state" data-testid="loop-state">{loop.state}</span>
      </td>
      <td>
        {loop.directory ? (
          <>
            <div>{shortPath(loop.directory)}</div>
            <div className="mono oc-loops-title-sub">{loop.directory}</div>
          </>
        ) : '—'}
      </td>
      <td className="oc-loops-td-when">
        <div>{loop.lastFiredAt ? relativeTime(loop.lastFiredAt) : 'never'}</div>
        <div className="oc-loops-title-sub" data-testid="loop-next-run">{next ? `next: ${next}` : ''}</div>
      </td>
      <td className="oc-loops-td-actions">
        <button className="vscode-btn" onClick={() => setHistory(true)} aria-label="Loop history">
          History
        </button>
        {!terminal && (
          <button className="vscode-btn" onClick={() => setEditing(true)} aria-label="Edit loop">
            Edit
          </button>
        )}
        {restartable && (
          <button
            className="vscode-btn"
            onClick={() => void restart(loop.id)}
            aria-label="Restart loop"
          >
            Restart
          </button>
        )}
      </td>
      {/* Portal the modals to <body> so their fixed-position backdrop
          isn't an invalid <div> child of <tr>. */}
      {editing && !terminal && createPortal(
        <LoopEditModal
          loop={loop}
          onSave={(req) => update(loop.id, req)}
          onPause={() => pause(loop.id)}
          onResume={() => resume(loop.id)}
          onTrigger={() => trigger(loop.id)}
          onDelete={() => remove(loop.id)}
          onClose={() => setEditing(false)}
        />,
        document.body,
      )}
      {history && createPortal(
        <LoopHistoryModal loop={loop} onClose={() => setHistory(false)} />,
        document.body,
      )}
    </tr>
  );
}
