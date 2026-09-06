import type { ReactNode } from 'react';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import { SearchSelect } from '../../components/SearchSelect';
import { useDashboard } from './context';

type Option = { value: string; label: string; icon?: ReactNode };

export function AnalyticsFilters({
  days,
  onDaysChange,
  agent,
  onAgentChange,
  agentOptions,
  model,
  onModelChange,
  modelOptions,
}: {
  days: number;
  onDaysChange: (days: number) => void;
  agent?: string;
  onAgentChange?: (agent: string) => void;
  agentOptions?: Option[];
  model?: string;
  onModelChange?: (model: string) => void;
  modelOptions?: Option[];
}) {
  const { projects, dirScope, setDirScope } = useDashboard();
  return (
    <div className="metrics-filters">
      <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} showLabel />
      {onAgentChange && (
        <label className="metrics-filter">
          <span>Agent</span>
          <SearchSelect value={agent ?? ''} ariaLabel="Agent" placeholder="All agents" searchLabel="Search agents" onChange={onAgentChange} options={agentOptions ?? [{ value: '', label: 'All agents' }]} />
        </label>
      )}
      {onModelChange && (
        <label className="metrics-filter">
          <span>Model</span>
          <SearchSelect value={model ?? ''} ariaLabel="Model" placeholder="All models" searchLabel="Search models" onChange={onModelChange} options={modelOptions ?? [{ value: '', label: 'All models' }]} />
        </label>
      )}
      <label className="metrics-filter metrics-filter-small">
        <span>Last</span>
        <SearchSelect
          value={String(days)}
          ariaLabel="Last"
          placeholder="Select range"
          searchLabel="Search ranges"
          onChange={(value) => onDaysChange(Number(value))}
          options={[
            { value: '7', label: '7 days' },
            { value: '30', label: '30 days' },
            { value: '90', label: '90 days' },
            { value: '0', label: 'All time' },
          ]}
        />
      </label>
    </div>
  );
}
