import { useEffect, useState } from 'react';
import './LoopsPane.css';
import type { Loop, LoopUpdateRequest } from '../lib/api.types';
import { useLoopsStore } from '../lib/loopsStore';
import { LoopEditModal } from './LoopEditModal';
import { LoopCreateModal } from './LoopCreateModal';
import { LoopHistoryView } from './LoopHistoryView';

interface LoopsPaneProps {
  directory: string | undefined;
  // Session id the pane is viewing; used to anchor new loops created here.
  sessionId?: string;
  platformId?: string;
  // RightPanel API parity (see UpstreamPane).
  onRefresh?: (refresh: () => void) => void;
  onLoadingChange?: (loading: boolean) => void;
}

/**
 * LoopsPane lists the agent loops anchored to the current project
 * directory inside the RightPanel. Each loop row expands to show its
 * iteration history (lazy-fetched on first expand). SSE `loop.updated`
 * broadcasts refresh the list automatically via the store.
 */
export function LoopsPane({ directory, sessionId, platformId, onRefresh, onLoadingChange }: LoopsPaneProps) {
  const loops = useLoopsStore((s) => s.loops);
  const loading = useLoopsStore((s) => s.loading);
  const error = useLoopsStore((s) => s.error);
  const load = useLoopsStore((s) => s.load);
  const create = useLoopsStore((s) => s.create);
  const [creating, setCreating] = useState(false);
  const remove = useLoopsStore((s) => s.remove);
  const pause = useLoopsStore((s) => s.pause);
  const resume = useLoopsStore((s) => s.resume);
  const trigger = useLoopsStore((s) => s.trigger);
  const update = useLoopsStore((s) => s.update);

  useEffect(() => {
    void load(directory ? { dir: directory } : {});
  }, [directory, load]);

  useEffect(() => {
    onLoadingChange?.(loading);
  }, [loading, onLoadingChange]);

  // Wire the pane-header refresh button to a directory-scoped reload.
  useEffect(() => {
    onRefresh?.(() => void load(directory ? { dir: directory } : {}));
  }, [onRefresh, load, directory]);

  const newLoopButton = sessionId ? (
    <button
      type="button"
      className="vscode-btn oc-loops-new-btn"
      data-testid="loops-new-btn"
      onClick={() => setCreating(true)}
    >
      + New loop
    </button>
  ) : null;

  return (
    <div className="oc-loops-pane" data-testid="loops-pane">
      {newLoopButton && <div className="oc-loops-pane-header">{newLoopButton}</div>}
      {error && <div className="oc-loops-pane-empty">{error}</div>}
      {!error && !loading && loops.length === 0 && (
        <div className="oc-loops-pane-empty" data-testid="loops-pane-empty">
          No loops for this project{sessionId ? '. Create one above.' : '.'}
        </div>
      )}
      {loops.map((loop) => (
        <LoopRow
          key={loop.id}
          loop={loop}
          onPause={() => pause(loop.id)}
          onResume={() => resume(loop.id)}
          onTrigger={() => trigger(loop.id)}
          onDelete={() => remove(loop.id)}
          onUpdate={(req) => update(loop.id, req)}
        />
      ))}
      {creating && sessionId && (
        <LoopCreateModal
          rootSessionId={sessionId}
          platform={platformId}
          directory={directory}
          onCreate={create}
          onClose={() => setCreating(false)}
        />
      )}
    </div>
  );
}

interface LoopRowProps {
  loop: Loop;
  onPause: () => Promise<void>;
  onResume: () => Promise<void>;
  onTrigger: () => Promise<void>;
  onDelete: () => Promise<void>;
  onUpdate: (req: LoopUpdateRequest) => Promise<void>;
}

function LoopRow({ loop, onPause, onResume, onTrigger, onDelete, onUpdate }: LoopRowProps) {
  const [editing, setEditing] = useState(false);
  const [showHistory, setShowHistory] = useState(false);

  const terminal = ['completed', 'deleted', 'errored'].includes(loop.state);

  return (
    <div className="oc-loops-row" data-testid="loop-row" data-loop-state={loop.state}>
      <div className="oc-loops-row-head">
        <span className="oc-loops-row-title">{loop.title || loop.id}</span>
        <span className="oc-loops-row-state" data-testid="loop-state">
          {loop.state}
        </span>
      </div>
      {loop.lastSummary && <div className="oc-loops-row-summary">{loop.lastSummary}</div>}
      <div className="oc-loops-row-actions">
        <button
          type="button"
          className="vscode-btn"
          onClick={() => setShowHistory((v) => !v)}
          aria-expanded={showHistory}
        >
          {showHistory ? 'Hide history' : 'History'}
        </button>
        {!terminal && (
          <button type="button" className="vscode-btn" onClick={() => setEditing(true)}>Edit</button>
        )}
      </div>

      {showHistory && <LoopHistoryView loop={loop} />}

      {editing && !terminal && (
        <LoopEditModal
          loop={loop}
          onSave={onUpdate}
          onPause={onPause}
          onResume={onResume}
          onTrigger={onTrigger}
          onDelete={onDelete}
          onClose={() => setEditing(false)}
        />
      )}
    </div>
  );
}


