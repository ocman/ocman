// Deliberate public wrappers around host components. Their props are the
// @ocman/plugin-ui contract, so internal components can change freely.
import type { ComponentPropsWithoutRef, ReactNode } from 'react';
import type { DataTableProps, PageProps, PickerProps, PillTone, RouteButtonProps, Severity, SubmitButtonProps, TabsProps } from '@ocman/plugin-ui';
import * as Control from '../components/Control';
import { DataTable as HostDataTable } from '../components/DataTable';
import { EmptyState as HostEmptyState } from '../components/EmptyState';
import { LoadingState as HostLoadingState } from '../components/LoadingState';
import { ErrorState as HostErrorState } from '../components/ErrorState';
import { Drawer as HostDrawer } from '../components/Drawer';
import * as HostTabs from '../components/Tabs';
import { StatusBadge as HostStatusBadge } from '../components/StatusBadge';
import { PluginProjectPicker } from '../components/PluginProjectPicker';
import { PluginModelPicker } from '../components/PluginModelPicker';
import { PluginPageValue } from '../components/PluginPageValue';
import { usePluginHost } from './context';
import '../pages/PluginPage.css';
import '../components/PluginPageContent.css';
import './plugin-ui.css';

export const Button = Control.Button;
export const ButtonGroup = Control.ButtonGroup;
export const TextField = Control.TextField;
export const CheckboxField = Control.CheckboxField;
export const TextareaField = Control.TextareaField;

export function SubmitButton({ onClick, ...props }: SubmitButtonProps) {
  return <Control.SubmitButton {...props} onClick={() => onClick()} />;
}

export function RouteButton(props: RouteButtonProps) {
  return <Control.RouteButton {...props} />;
}

export function SearchField(props: ComponentPropsWithoutRef<'input'>) {
  return <Control.SearchField {...props} />;
}

export function SelectField(props: ComponentPropsWithoutRef<'select'>) {
  return <Control.SelectField {...props} />;
}

export function ProjectPicker(props: PickerProps) {
  return <PluginProjectPicker ownerId={usePluginHost().ownerId} {...props} />;
}

export function ModelPicker(props: PickerProps) {
  return <PluginModelPicker ownerId={usePluginHost().ownerId} {...props} />;
}

export function DataTable(props: DataTableProps) {
  return <HostDataTable {...props} />;
}

export function EmptyState(props: ComponentPropsWithoutRef<'div'>) {
  return <HostEmptyState {...props} />;
}

export function LoadingState({ children }: { children: ReactNode }) {
  return <HostLoadingState>{children}</HostLoadingState>;
}

export function ErrorState(props: ComponentPropsWithoutRef<typeof HostErrorState>) {
  return <HostErrorState {...props} />;
}

export function Drawer({ title, onClose, closeLabel, children }: { title: string; onClose(): void; closeLabel?: string; children: ReactNode }) {
  return <HostDrawer title={title} onClose={onClose} closeLabel={closeLabel}>{children}</HostDrawer>;
}

export function Tabs(props: TabsProps) {
  return <HostTabs.Tabs {...props} />;
}

export function TabsList(props: { 'aria-label': string; children: ReactNode }) {
  return <HostTabs.TabsList {...props} />;
}

export function TabsTrigger(props: { value: string; disabled?: boolean; children: ReactNode }) {
  return <HostTabs.TabsTrigger {...props} />;
}

export function TabsContent(props: { value: string; children: ReactNode }) {
  return <HostTabs.TabsContent {...props} />;
}

export function StatusBadge({ status, compact }: { status: string; compact?: boolean }) {
  return <HostStatusBadge status={status} compact={compact} />;
}

const severities: Severity[] = ['critical', 'high', 'medium', 'low', 'info', 'unknown'];

export function SeverityPill({ severity }: { severity: Severity }) {
  return <PluginPageValue value={severities.includes(severity) ? severity : 'unknown'} display="severity" />;
}

export function Pill({ tone = 'neutral', children }: { tone?: PillTone; children: ReactNode }) {
  return <span className={`oc-plugin-pill oc-plugin-pill--${tone}`}>{children}</span>;
}

export function Page({ title, description, actions, children }: PageProps) {
  return <section className="plugin-page">
    <div className="plugin-page-heading"><div><h1>{title}</h1>{description && <p>{description}</p>}</div>{actions}</div>
    {children}
  </section>;
}

export function Section({ title, description, actions, children }: PageProps) {
  return <section aria-label={title} className="oc-plugin-section">
    <div className="plugin-page-heading"><div><h2>{title}</h2>{description && <p>{description}</p>}</div>{actions}</div>
    {children}
  </section>;
}

export function Properties({ items }: { items: { label: string; value: ReactNode }[] }) {
  return <HostDataTable framed><tbody>{items.map((item, i) => <tr key={i}>
    <th scope="row">{item.label}</th><td className="plugin-detail-value">{item.value}</td>
  </tr>)}</tbody></HostDataTable>;
}

export function CodeBlock({ children, language }: { children: ReactNode; language?: string }) {
  return <pre className="oc-plugin-code" data-language={language}><code>{children}</code></pre>;
}
