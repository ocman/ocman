import { useEffect, useMemo, useRef } from 'react';
import type { AgentInfo, Message, Part } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { getProjectModel, saveProjectModel } from '../../lib/projectModel';
import { getSessionModel, saveSessionModel } from '../../lib/sessionModel';
import { agentModelRef, deriveActiveModelAndAgent, formatModelRef } from '../../lib/sessionStatus';
import { computeTurnStats, latestTurnModel, messageModelRef } from '../../lib/turnStats';
import { startModels } from './startHandoffs';
import { modelRefForSession } from '../../lib/modelRef';

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
  /** Latest requested model, falling back to the turn and session defaults. */
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

  // Prefer explicit session choices over models observed in message history.
  // A new session seeds the project's configured default, then the
  // last pick in this project, then the session's default model.
  const turnStatsMap = computeTurnStats(messages, parts);
  const { activeAgent } = deriveActiveModelAndAgent(messages, session);
  const sessionModel = id ? getSessionModel(id) : '';
  const activeModel = useMemo(
    () => {
      const latestUser = messages.findLast((message) => message.data?.role === 'user');
      // New-conversation and project picks may name their platform; this
      // session takes only its own platform's (prefix stripped).
      const own = (ref: string | undefined) => formatModelRef(undefined, modelRefForSession(ref || '', session?.platform));
      const projectModel = own(session?.projectDefaultModel) || own(getProjectModel(session?.directory || ''));
      return own(session?.id === id && id ? startModels.get(id) : '') ||
        own(sessionModel) ||
        (latestUser ? messageModelRef(latestUser) : '') ||
        latestTurnModel(messages, turnStatsMap) ||
        (messages.length === 0 ? projectModel : '') ||
        own(session?.defaultModel) ||
        projectModel ||
        '';
    },
    [id, sessionModel, messages, turnStatsMap, session?.id, session?.directory, session?.projectDefaultModel, session?.defaultModel, session?.platform],
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
    if (startModels.has(id)) saveSessionModel(id, activeModel);
    startModels.delete(id);
    seededSessionRef.current = id;
  }, [activeModel, id, session?.id, setSelectedModel]);

  const composerModels = Array.from(new Set([activeModel, session?.defaultModel, ...modelOptions].map((model) => formatModelRef(undefined, model)).filter(Boolean)));

  const directory = session?.directory;
  const handleModelChange = (model: string) => {
    seededSessionRef.current = id;
    if (id) {
      saveSessionModel(id, model);
      startModels.delete(id);
    }
    setSelectedModel(model);
    setSelectedReasoning('');
    if (directory) saveProjectModel(directory, model);
  };

  // Switching to an agent that defines a model selects that model in
  // the composer (and thus respects it on send). A later manual model
  // change overrides it; switching agents again re-applies the new
  // agent's model. Agents without a model leave the selection as-is.
  const handleAgentChange = (agent: string) => {
    seededSessionRef.current = id;
    setSelectedAgent(agent);
    const agentModel = agentModelRef(agents.find((a) => a.name === agent));
    if (agentModel) {
      if (id) {
        saveSessionModel(id, agentModel);
        startModels.delete(id);
      }
      setSelectedModel(agentModel);
      setSelectedReasoning('');
    }
  };

  return { activeAgent, activeModel, composerModels, handleModelChange, handleAgentChange };
}
