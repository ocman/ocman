import { useUiStore } from '../../lib/uiStore';
import type { SlashCommand } from '../../lib/api';
import type { ComposerProps } from './composerTypes';
import type { useComposerPickers } from './useComposerPickers';
import { ModelPicker } from './ModelPicker';
import { AgentPicker } from './AgentPicker';
import { SkillPicker } from './SkillPicker';
import { RoutinePicker } from './RoutinePicker';
import { ReasoningPicker } from './ReasoningPicker';
import { HelpDialog } from './HelpDialog';

type Props = Pick<ComposerProps, 'models' | 'modelEntries' | 'onModelChange' | 'onToggleFavorite'
  | 'agents' | 'activeAgent' | 'onAgentChange' | 'selectedReasoning' | 'onReasoningChange'> & {
  pickers: ReturnType<typeof useComposerPickers>;
  effectiveModel: string;
  effectiveAgent: string;
  agentOptions: string[];
  commands: SlashCommand[];
  reasoningOptions: string[];
};

export function ComposerDialogs({
  pickers, models, modelEntries, effectiveModel, onModelChange, onToggleFavorite,
  agentOptions, agents, activeAgent, effectiveAgent, onAgentChange, commands,
  reasoningOptions, selectedReasoning, onReasoningChange,
}: Props) {
  return <>
    {pickers.model.open && <ModelPicker open models={models || []} modelEntries={modelEntries}
      currentModel={effectiveModel} initialQuery={pickers.model.query} onSelect={(m) => onModelChange?.(m)}
      onToggleFavorite={onToggleFavorite} onClose={pickers.model.close}
      onBack={() => { pickers.model.setOpen(false); useUiStore.getState().openPalette('command'); }} />}
    {pickers.agent.open && <AgentPicker open agentNames={agentOptions} agents={agents}
      activeAgent={activeAgent} currentAgent={effectiveAgent} initialQuery={pickers.agent.query}
      onSelect={(a) => onAgentChange?.(a)} onClose={pickers.agent.close} />}
    {pickers.skill.open && <SkillPicker open commands={commands} initialQuery={pickers.skill.query}
      onSelect={pickers.insertSkill} onClose={pickers.skill.close} />}
    {pickers.routine.open && <RoutinePicker open initialQuery={pickers.routine.query}
      onSelect={pickers.insertRoutine} onClose={pickers.routine.close} />}
    {pickers.reasoning.open && <ReasoningPicker open options={reasoningOptions} current={selectedReasoning}
      onSelect={(v) => onReasoningChange?.(v)} onClose={pickers.reasoning.close} />}
    {pickers.help.open && <HelpDialog open commands={commands} onClose={pickers.help.close} />}
  </>;
}
