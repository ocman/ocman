// @ocman/plugin-ui 1.1 wrappers for host page states, lists, dates and icons.
import type { ReactNode } from 'react';
import { PageState as HostPageState } from '../components/PageState';
import { InlineAlert as HostInlineAlert } from '../components/InlineAlert';
import { SkeletonLines as HostSkeletonLines, SkeletonTable as HostSkeletonTable } from '../components/Skeleton';
import { RelativeTimestamp } from '../components/RelativeTime';
import { DataTableGroup as HostDataTableGroup, DataTableRow as HostDataTableRow } from '../components/DataTable';
import { RouteTabs as HostRouteTabs } from '../components/RouteTabs';
import { usePluginHost } from './context';

const iconName = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;

export function Icon({ name, label }: { name: string; label?: string }) {
  if (!iconName.test(name)) return null;
  return label
    ? <i className={`bi bi-${name}`} role="img" aria-label={label} />
    : <i className={`bi bi-${name}`} aria-hidden="true" />;
}

export function PageState({ title, description, icon, error = false, children }: { title: string; description: ReactNode; icon?: string; error?: boolean; children?: ReactNode }) {
  const name = icon && iconName.test(icon) ? icon : error ? 'exclamation-triangle' : 'inbox';
  return <HostPageState title={title} description={description} icon={`bi-${name}`} error={error}>{children}</HostPageState>;
}

export function InlineAlert({ children, onRetry, retrying, compact }: { children: ReactNode; onRetry?(): void; retrying?: boolean; compact?: boolean }) {
  return <HostInlineAlert onRetry={onRetry} retrying={retrying} compact={compact}>{children}</HostInlineAlert>;
}

export function SkeletonTable({ columns, rows, label }: { columns: string[]; rows?: number; label: string }) {
  return <HostSkeletonTable columns={columns} rows={rows} label={label} />;
}

export function SkeletonLines({ rows, label }: { rows?: number; label: string }) {
  return <HostSkeletonLines rows={rows} label={label} />;
}

export function RelativeTime({ value }: { value: string | number | Date }) {
  return <RelativeTimestamp value={value} />;
}

export function DataTableGroup({ label, noun, count, children }: { label: string; noun: string; count: number; children: ReactNode }) {
  return <HostDataTableGroup label={label} noun={noun} count={count}>{children}</HostDataTableGroup>;
}

export function DataTableRow({ primary, secondary, meta }: { primary: ReactNode; secondary?: ReactNode; meta?: ReactNode }) {
  return <HostDataTableRow primary={primary} secondary={secondary} meta={meta} />;
}

// Plugins render under a splat route, where relative links resolve from the
// full URL; resolve tabs from the plugin root so they work at any depth.
export function RouteTabs({ label, tabs }: { label: string; tabs: { to: string; label: string; end?: boolean }[] }) {
  const { basePath } = usePluginHost();
  return <HostRouteTabs label={label} tabs={tabs.map((tab) => ({ ...tab, to: tab.to.startsWith('/') ? tab.to : `${basePath}/${tab.to}`.replace(/\/$/, '') }))} />;
}
