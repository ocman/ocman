import { useState, type ReactNode } from 'react';
import { ProjectScopePicker } from '../../components/ProjectScopePicker';
import { SearchSelect } from '../../components/SearchSelect';
import { FilterField } from '../../components/FilterField';
import { Button } from '../../components/Control';
import { Drawer } from '../../components/Drawer';
import { ModalFooter } from '../../components/ModalFooter';
import { shortPath } from '../../lib/format';
import { useIsPhone } from '../../lib/useIsPhone';
import { useDashboard } from './context';
import './AnalyticsFilters.css';

type Option = { value: string; label: string; icon?: ReactNode };

const RANGES = [
  { value: '7', label: '7 days' },
  { value: '30', label: '30 days' },
  { value: '90', label: '90 days' },
  { value: '0', label: 'All time' },
];

type Props = {
  days: number;
  onDaysChange: (days: number) => void;
  agent?: string;
  onAgentChange?: (agent: string) => void;
  agentOptions?: Option[];
  model?: string;
  onModelChange?: (model: string) => void;
  modelOptions?: Option[];
};

/**
 * Analytics filters. Desktop shows the fields inline; phones show one
 * summary row that opens the same fields in a sheet, so four full-width
 * selects no longer fill the first screen.
 */
export function AnalyticsFilters(props: Props) {
  const isPhone = useIsPhone();
  const [open, setOpen] = useState(false);
  const { dirScope } = useDashboard();
  if (!isPhone) return <div className="analytics-filters"><FilterFields {...props} /></div>;

  const label = (options: Option[] | undefined, value: string | undefined) => options?.find((option) => option.value === value)?.label ?? value;
  const active = [dirScope, props.agent, props.model].filter(Boolean).length;
  const summary = [
    dirScope ? shortPath(dirScope) : 'All projects',
    props.agent && label(props.agentOptions, props.agent),
    props.model && label(props.modelOptions, props.model),
    RANGES.find((range) => range.value === String(props.days))?.label,
  ].filter(Boolean).join(' · ');
  return (
    <div className="analytics-filter-bar">
      <span className="analytics-filter-summary">{summary}</span>
      <Button size="small" onClick={() => setOpen(true)} aria-haspopup="dialog">
        <i className="bi bi-sliders" aria-hidden="true" /> Filters{active > 0 ? ` (${active})` : ''}
      </Button>
      {open && (
        <Drawer title="Filters" onClose={() => setOpen(false)}>
          <div className="analytics-filter-sheet"><FilterFields {...props} /></div>
          <ModalFooter label="Filter actions"><Button variant="accent" onClick={() => setOpen(false)}>Done</Button></ModalFooter>
        </Drawer>
      )}
    </div>
  );
}

function FilterFields({ days, onDaysChange, agent, onAgentChange, agentOptions, model, onModelChange, modelOptions }: Props) {
  const { projects, dirScope, setDirScope } = useDashboard();
  return <>
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
      <SearchSelect value={String(days)} ariaLabel="Last" placeholder="Select range" searchLabel="Search ranges" onChange={(value) => onDaysChange(Number(value))} options={RANGES} />
    </FilterField>
  </>;
}
