import type { ReactNode, Ref } from 'react';
import type { AgentInfo, SessionModelEntry } from '../../lib/api';
import type { AttachedImage } from './useComposerAttachments';
import type { SessionTarget } from './ComposerSelectorRow';
import type { TargetCandidate } from '../../lib/api.types';

export interface ComposerHandle {
  openModelPicker: (query?: string) => void;
  openAgentPicker: (query?: string) => void;
}

export interface ComposerProps {
  /** Ctrl/Cmd+Enter holds the prompt for the next idle edge. */
  onSend?: (text: string, images?: AttachedImage[], queue?: boolean) => void | Promise<void>;
  onRetryChange?: (delaySeconds: number | null) => void;
  onCommand?: (command: string, args: string) => void | Promise<void>;
  /** Shell commands arrive with the leading ! removed. */
  onShell?: (command: string) => void | Promise<void>;
  shellExec?: boolean;
  queuedShellCommand?: string | null;
  onCancelQueuedShell?: () => void;
  queuedMessages?: { id: string; text: string; hasImages: boolean }[];
  onRemoveQueuedMessage?: (id: string) => void;
  onMoveQueuedMessage?: (id: string, direction: -1 | 1) => void;
  onAbort?: () => void;
  isRunning: boolean;
  disabled?: boolean;
  disabledHint?: string;
  whisperAvailable?: boolean;
  models?: string[];
  modelEntries?: SessionModelEntry[];
  selectedModel?: string;
  onModelChange?: (model: string) => void;
  onToggleFavorite?: (provider: string, model: string, nextFavorite: boolean) => void;
  /** Refresh the session-scoped catalog whenever its picker opens. */
  onRefreshModels?: () => void;
  activeAgent?: string;
  selectedAgent?: string;
  onAgentChange?: (agent: string) => void;
  agents?: AgentInfo[];
  /** Accent colours remain muted until the authoritative catalog resolves. */
  agentsLoaded?: boolean;
  contextTokens?: number;
  activeDurationMs?: number;
  timeCreated?: number;
  durationMs?: number;
  sessionId?: string;
  tokensPerSecond?: number;
  tokenStats?: {
    input: number;
    output: number;
    reasoning: number;
    cacheRead: number;
    cacheWrite: number;
    totalCost: number;
    contextWindow?: number;
  };
  estimatedCost?: number;
  sessionTreeStats?: {
    input: number;
    output: number;
    totalCost: number;
    totalEstCost: number;
    totalEffectiveCost: number;
    sessions: number;
  };
  selectedReasoning?: string;
  onReasoningChange?: (reasoning: string) => void;
  onLaunchRequest?: () => void;
  launching?: boolean;
  directory?: string;
  /** The session target can change only before its first message. */
  newConversation?: boolean;
  worktreesSupported?: boolean;
  target?: SessionTarget;
  onTargetChange?: (target: SessionTarget) => void;
  permissionControl?: ReactNode;
  composerRef?: Ref<ComposerHandle>;
  remoteId?: string;
  onMachineChange?: (target: TargetCandidate, draft: string) => Promise<void>;
}
