import { useEffect } from 'react';
import { usePluginPaneTree, type PluginPane as PluginPaneDescriptor } from '../lib/pluginPanes';
import { PluginTreePane } from './PluginTreePane';
import { LoadingState } from './LoadingState';
import { Button } from './Control';

// Mounted only for open panes. Unmounting consumes the query's AbortSignal,
// cancelling both the HTTP request and the owner-local plugin call.
export function PluginPane({ pane, directory, onRefresh, onLoadingChange }: {
  pane: PluginPaneDescriptor;
  directory: string | undefined;
  onRefresh: (refresh: () => void) => void;
  onLoadingChange: (loading: boolean) => void;
}) {
  const result = usePluginPaneTree(pane, directory);
  const { refetch } = result;
  useEffect(() => onRefresh(() => { void refetch(); }), [onRefresh, refetch]);
  useEffect(() => onLoadingChange(result.isFetching), [onLoadingChange, result.isFetching]);
  if (!directory) return <p role="status">Waiting for the project.</p>;
  if (!result.data && result.error) return (
    <div role="alert">
      Could not load {pane.pane.label}.
      <Button variant="ghost" onClick={() => void result.refetch()}>Retry</Button>
    </div>
  );
  if (!result.data) return <LoadingState>Loading {pane.pane.label}…</LoadingState>;
  return <PluginTreePane label={pane.pane.label} status={result.data} loading={result.isFetching} error={result.error} refresh={result.refetch} />;
}
