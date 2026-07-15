// The memo equality predicate for <Composer>. Split into its own file so
// it can be unit-tested and imported without tripping react-refresh's
// "only export components" rule. Returns true when a re-render can be
// skipped. Must include every prop that affects the rendered output
// (e.g. queuedMessages) or that view silently goes stale.

interface ModelEntry {
  provider: string;
  model: string;
  isAvailable?: boolean;
}

interface TokenStats {
  input?: number;
  output?: number;
  totalCost?: number;
}

// The subset of Composer props the comparator inspects. Kept structural
// (not tied to the component's full prop type) so this file has no import
// cycle with Composer.tsx.
export interface ComposerMemoProps {
  isRunning?: boolean;
  queuedShellCommand?: string | null;
  onCancelQueuedShell?: unknown;
  disabled?: boolean;
  disabledHint?: string;
  queuedMessages?: unknown;
  onLaunchRequest?: unknown;
  launching?: boolean;
  whisperAvailable?: boolean;
  selectedModel?: unknown;
  activeAgent?: unknown;
  selectedAgent?: unknown;
  agents?: unknown;
  agentsLoaded?: boolean;
  contextTokens?: unknown;
  activeDurationMs?: number;
  sessionId?: string;
  tokensPerSecond?: unknown;
  tokenStats?: TokenStats;
  selectedReasoning?: unknown;
  directory?: string;
  newConversation?: boolean;
  worktreesSupported?: boolean;
  permissionControl?: unknown;
  models?: unknown[];
  modelEntries?: ModelEntry[];
}

export const composerPropsEqual = (prev: ComposerMemoProps, next: ComposerMemoProps) =>
  prev.isRunning === next.isRunning &&
  prev.queuedShellCommand === next.queuedShellCommand &&
  prev.onCancelQueuedShell === next.onCancelQueuedShell &&
  prev.disabled === next.disabled &&
  prev.disabledHint === next.disabledHint &&
  // Follow-up queue must re-render when it changes or the session switches;
  // the queue array identity changes on every refetch (useMessageQueue).
  prev.queuedMessages === next.queuedMessages &&
  prev.onLaunchRequest === next.onLaunchRequest &&
  prev.launching === next.launching &&
  prev.whisperAvailable === next.whisperAvailable &&
  prev.selectedModel === next.selectedModel &&
  prev.activeAgent === next.activeAgent &&
  prev.selectedAgent === next.selectedAgent &&
  prev.agents === next.agents &&
  prev.agentsLoaded === next.agentsLoaded &&
  prev.contextTokens === next.contextTokens &&
  prev.activeDurationMs === next.activeDurationMs &&
  prev.sessionId === next.sessionId &&
  prev.tokensPerSecond === next.tokensPerSecond &&
  prev.tokenStats?.input === next.tokenStats?.input &&
  prev.tokenStats?.output === next.tokenStats?.output &&
  prev.tokenStats?.totalCost === next.tokenStats?.totalCost &&
  prev.selectedReasoning === next.selectedReasoning &&
  prev.directory === next.directory &&
  prev.newConversation === next.newConversation &&
  prev.worktreesSupported === next.worktreesSupported &&
  prev.permissionControl === next.permissionControl &&
  (prev.models?.length || 0) === (next.models?.length || 0) &&
  (prev.models || []).every((model, i) => model === (next.models || [])[i]) &&
  // Re-render when model availability data changes so the "not available"
  // warning on the model button appears/clears once entries load.
  (prev.modelEntries?.length || 0) === (next.modelEntries?.length || 0) &&
  (prev.modelEntries || []).every((e, i) => {
    const n = (next.modelEntries || [])[i];
    return !!n && e.provider === n.provider && e.model === n.model && e.isAvailable === n.isAvailable;
  });
