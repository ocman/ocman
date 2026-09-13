import { useCallback, useEffect, useMemo, useRef } from 'react';
import type { AgentInfo, Message, Part } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { getProjectModel, saveProjectModel } from '../../lib/projectModel';
import { agentModelRef, deriveActiveModelAndAgent } from '../../lib/sessionStatus';
import { computeTurnStats, latestTurnModel } from '../../lib/turnStats';

export interface UseComposerModelOptions {
  /** Route id — the seed is keyed on this, not on `session.id`. */
  id: string | undefined;
  session: SessionMetadata | null;
  messages: Message[];
  parts: Part[];
  modelOptions: string[];
  agents: AgentInfo[];
  setSelectedModel: (model: string) => void;
  setSelectedAgent: (agent: string) => void;
  setSelectedReasoning: (reasoning: string) => void;
}

export interface UseComposerModelResult {
  /** Agent behind the most recent assistant turn. */
  activeAgent: string;
  /** Model the session is currently on (latest turn → project → session default). */
  activeModel: string;
  /** Model picker list: active + default + catalog, deduped. */
  composerModels: string[];
  handleModelChange: (model: string) => void;
  handleAgentChange: (agent: string) => void;
}

/**
 * Derives the session's active model/agent and pre-seeds the composer's
 * model selection exactly once per session id. The composer's
 * `selectedModel` is the single source of truth for the next message;
 * seeding once means later assistant responses can't move the selection.
 */
export function useComposerModel({
  id,
  session,
  messages,
  parts,
  modelOptions,
  agents,
  setSelectedModel,
  setSelectedAgent,
  setSelectedReasoning,
}: UseComposerModelOptions): UseComposerModelResult {
  const seededSessionRef = useRef<string | undefined>(undefined);
  useEffect(() => {
    seededSessionRef.current = undefined;
  }, [id]);

  // Prefer the model behind the most recent turn (what OpenCode will keep
  // using), falling back to the session's default model.
  const turnStatsMap = useMemo(() => computeTurnStats(messages, parts), [messages, parts]);
  const { activeAgent } = useMemo(
    () => deriveActiveModelAndAgent(messages, session),
    [messages, session],
  );
  const activeModel = useMemo(
    () =>
      latestTurnModel(messages, turnStatsMap) ||
      (messages.length === 0 ? getProjectModel(session?.directory || '') : '') ||
      session?.defaultModel ||
      '',
    [messages, turnStatsMap, session?.directory, session?.defaultModel],
  );

  useEffect(() => {
    if (!id) return;
    if (seededSessionRef.current === id) return;
    // The page is not remounted on navigation, so for a render or two
    // after `id` flips `session`/`messages` still hold the previously
    // viewed session. Seeding from those would latch the old session's
    // model and the one-shot guard would then block the correct one.
    if (session?.id !== id) return;
    if (!activeModel) return;
    setSelectedModel(activeModel);
    seededSessionRef.current = id;
  }, [activeModel, id, session?.id, setSelectedModel]);

  const composerModels = useMemo(
    () => Array.from(new Set([activeModel, session?.defaultModel, ...modelOptions].filter((model): model is string => !!model))),
    [activeModel, session?.defaultModel, modelOptions],
  );

  const directory = session?.directory;
  const handleModelChange = useCallback((model: string) => {
    seededSessionRef.current = id;
    setSelectedModel(model);
    setSelectedReasoning('');
    if (directory) saveProjectModel(directory, model);
  }, [id, directory, setSelectedModel, setSelectedReasoning]);

  // Switching to an agent that defines a model selects that model in
  // the composer (and thus respects it on send). A later manual model
  // change overrides it; switching agents again re-applies the new
  // agent's model. Agents without a model leave the selection as-is.
  const handleAgentChange = useCallback((agent: string) => {
    seededSessionRef.current = id;
    setSelectedAgent(agent);
    const agentModel = agentModelRef(agents.find((a) => a.name === agent));
    if (agentModel) {
      setSelectedModel(agentModel);
      setSelectedReasoning('');
    }
  }, [agents, id, setSelectedAgent, setSelectedModel, setSelectedReasoning]);

  return { activeAgent, activeModel, composerModels, handleModelChange, handleAgentChange };
}
