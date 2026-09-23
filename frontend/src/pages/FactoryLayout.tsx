import { useContext, type ReactNode } from 'react';
import { Link, NavLink } from 'react-router-dom';
import { Button, SearchField } from '../components/Control';
import { DataTableRow } from '../components/DataTable';
import { Modal } from '../components/Modal';
import type { FactoryIssue } from '../lib/api';
import { EpicCell, ProjectCell, type EpicRef } from './FactoryIssues';
import { OpenIssueContext, type DispatchEvidence } from './factoryHelpers';

export function QueryError({ error, retry }: { error: unknown; retry: () => void }) {
  return <div className="oc-error-banner" role="alert">
    {error instanceof Error ? error.message : 'Factory data is unavailable.'}
    <Button type="button" onClick={retry}>Retry</Button>
  </div>;
}

export function BlockerEvidence({ blockers }: { blockers?: FactoryIssue['blockers'] }) {
  if (!blockers?.length) return null;
  return <>{blockers.map(({ id, epicId, type, outcome, reason }, index) => <span key={id}>{index > 0 && '; '}{type === 'merge_gated' && 'merge gate on '}<Link to={`/factory/issues/${encodeURIComponent(id)}`} aria-label={`Open blocker ${id}`}>{id}{epicId ? ` in Work Epic ${epicId}` : ''}</Link> {outcome || 'pending'}{reason ? `: ${reason}` : ''}</span>)}</>;
}

export function DispatchExplanation({ item }: { item: DispatchEvidence }) {
  const blocker = <BlockerEvidence blockers={item.blockers} />;
  const hasBlocker = Boolean(item.blockers?.length);
  switch (item.dispatchState) {
    case 'terminally_blocked': return <span>Dispatch: cannot proceed because {hasBlocker ? blocker : 'a prerequisite failed'}.</span>;
    case 'not_applicable': return <span>Dispatch: {item.outcomeReason || <>not applicable because the recovery condition was not met{hasBlocker && <> ({blocker})</>}.</>}</span>;
    case 'deferred': return <span>Dispatch: delayed{item.outcomeReason ? `: ${item.outcomeReason}` : ''}.</span>;
    case 'retry_wait': return <span>Dispatch: retry {item.retryAttempts ?? 0} scheduled for {item.retryAt ? new Date(item.retryAt).toISOString() : 'a later time'}.</span>;
    case 'waiting': return <span>Dispatch: waiting for prerequisites{hasBlocker && <>: {blocker}</>}.</span>;
    case 'ready': return <span>Dispatch: ready.</span>;
    case 'running': return <span>Dispatch: in progress.</span>;
    case 'completed': return <span>Dispatch: complete.</span>;
    case 'reference': return <span>Dispatch: reference work is not scheduled.</span>;
		case 'paused': return <span>Dispatch: epic paused.</span>;
    default: return null;
  }
}

export function FactoryDataRow({ id, idLabel = 'Issue', title, epic, detail, actions }: { id: string; idLabel?: string; title: ReactNode; epic?: EpicRef; detail?: ReactNode; actions?: ReactNode }) {
	const openIssue = useContext(OpenIssueContext);
	const primary = openIssue && idLabel === 'Issue' ? <button type="button" aria-label={`Open issue ${id}`} onClick={() => openIssue(id)}>{title}</button> : title;
	return <DataTableRow className="factory-grid-row" primary={primary} secondary={<><span className="oc-data-table-field-label">{idLabel} </span>{epic ? <Link to={`/factory/epics/${encodeURIComponent(epic.id)}`}>{id}</Link> : id}</>} meta={<><ProjectCell path={epic?.initialProject} /><EpicCell id={epic?.id} goal={epic?.goal} /><div className="factory-row-detail">{detail}</div><div className="factory-cell factory-cell--actions">{actions}</div></>} />;
}


export function Drawer({ title, onClose, children }: { title: string; onClose: () => void; children: ReactNode }) {
  return <Modal label={title} onClose={onClose} backdropClassName="factory-issue-backdrop" dialogClassName="factory-issue-drawer factory-form-drawer">
    <header className="factory-form-drawer-header"><h2>{title}</h2><button className="factory-issue-close" type="button" onClick={onClose} aria-label={`Close ${title}`} title="Close"><i className="bi bi-x-lg" aria-hidden="true" /></button></header>
    {children}
  </Modal>;
}

export function InventoryToolbar({ label, value, onChange, children }: { label: string; value: string; onChange: (value: string) => void; children?: ReactNode }) {
  return <div className="factory-toolbar factory-filter-bar" role="search"><label>{label}<SearchField value={value} onChange={(event) => onChange(event.target.value)} /></label>{children}</div>;
}

export function FactoryPage({ children }: { children: ReactNode }) {
  return <main className="factory-page"><nav aria-label="Factory"><NavLink to="/factory/overview">Overview</NavLink><NavLink to="/factory/epics">Epics</NavLink><NavLink to="/factory/issues">Issues</NavLink><NavLink to="/factory/queue">Queue</NavLink><NavLink to="/factory/configuration">Configuration</NavLink><NavLink to="/factory/how-to" className={({ isActive }) => `factory-how-to-link${isActive ? ' active' : ''}`}><i className="bi bi-book" aria-hidden="true" />How to</NavLink></nav>{children}</main>;
}
