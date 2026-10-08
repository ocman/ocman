import type { ReactNode } from 'react';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import { SearchSelect } from '../../components/SearchSelect';
import { FilterField } from '../../components/FilterField';
import { useDashboard } from './context';
import './AnalyticsFilters.css';

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
    <div className="analytics-filters">
      <ProjectScopePicker projects={projects} value={dirScope} onChange={setDirScope} showLabel />
      {onAgentChange && (
        <FilterField label="Agent">
          <SearchSelect value={agent ?? ''} ariaLabel="Agent" placeholder="All agents" searchLabel="Search agents" onChange={onAgentChange} options={agentOptions ?? [{ value: '', label: 'All agents' }]} />
        </FilterField>
      )}
      {onModelChange && (
        <FilterField label="Model">
          <SearchSelect value={model ?? ''} ariaLabel="Model" placeholder="All models" searchLabel="Search models" onChange={onModelChange} options={modelOptions ?? [{ value: '', label: 'All models' }]} />
        </FilterField>
      )}
      <FilterField label="Last" compact>
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
      </FilterField>
    </div>
  );
}
