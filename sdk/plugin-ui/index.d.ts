// @ocman/plugin-ui 1.0.0: the public, semver-versioned surface a plugin UI
// bundle may import. The host provides the implementation at runtime; plugin
// builds keep this module external (see @ocman/plugin-vite). Additions are
// minor releases; removals or changed meaning are major releases.
import type { ComponentPropsWithoutRef, ComponentPropsWithRef, JSX, ReactNode } from 'react';
import type { UseMutationResult, UseQueryResult } from '@tanstack/react-query';

export declare const PLUGIN_UI_VERSION: string;

// --- Plugin context and data access -------------------------------------

export interface PluginContextValue {
  /** The plugin's own reverse-domain ID. */
  pluginId: string;
  /** The owner the plugin executes on. In-app UI is only loaded for `local`. */
  ownerId: string;
  /** Absolute route of the plugin's index, e.g. `/plugins/org.example.catalog`. */
  basePath: string;
}

export declare function usePluginContext(): PluginContextValue;

export interface PluginCallOptions {
  enabled?: boolean;
  refetchInterval?: number | false;
}

/** Calls a plugin-defined ui.v1 method of this plugin, as a cached query. */
export declare function usePluginCall<T>(method: string, params?: unknown, options?: PluginCallOptions): UseQueryResult<T, Error>;

/** Calls a plugin-defined ui.v1 method imperatively. Success refreshes this plugin's queries. */
export declare function usePluginMutation<T, P = unknown>(method: string): UseMutationResult<T, Error, P>;

export interface PluginActionHandle {
  /** Runs the action through ocman's action dialog, including confirmations, notices and artifacts. Resolves when the dialog closes. */
  run(context?: Record<string, string>): Promise<void>;
  pending: boolean;
}

/** One of this plugin's declared actions. */
export declare function usePluginAction(actionId: string): PluginActionHandle;

// --- Controls -------------------------------------------------------------

export type ButtonVariant = 'default' | 'accent' | 'muted' | 'link' | 'ghost' | 'danger';
export type ButtonSize = 'compact' | 'small' | 'normal' | 'large';
export type ButtonProps = ComponentPropsWithRef<'button'> & { variant?: ButtonVariant; size?: ButtonSize };
export type SubmitButtonProps = Omit<ButtonProps, 'onClick'> & {
  /** The button stays busy until the returned promise settles. */
  onClick: () => unknown;
  pending?: boolean;
  pendingLabel?: ReactNode;
};
export type RouteButtonProps = Omit<ComponentPropsWithoutRef<'a'>, 'href'> & {
  /** A route path, relative to the current plugin route unless it starts with `/`. */
  to: string;
  variant?: ButtonVariant;
  size?: ButtonSize;
};
export type ButtonGroupProps = Omit<ComponentPropsWithoutRef<'div'>, 'role' | 'aria-label'> & { label: string; joined?: boolean };

export declare function Button(props: ButtonProps): JSX.Element;
export declare function SubmitButton(props: SubmitButtonProps): JSX.Element;
export declare function RouteButton(props: RouteButtonProps): JSX.Element;
export declare function ButtonGroup(props: ButtonGroupProps): JSX.Element;
export declare function TextField(props: ComponentPropsWithRef<'input'>): JSX.Element;
export declare function SearchField(props: ComponentPropsWithoutRef<'input'>): JSX.Element;
export declare function SelectField(props: ComponentPropsWithoutRef<'select'>): JSX.Element;
export declare function CheckboxField(props: Omit<ComponentPropsWithRef<'input'>, 'type'>): JSX.Element;
export declare function TextareaField(props: ComponentPropsWithRef<'textarea'>): JSX.Element;

export interface PickerProps {
  label: string;
  value: string;
  onChange(value: string): void;
}

/** Owner-local project picker. `value` holds newline-separated directories. */
export declare function ProjectPicker(props: PickerProps): JSX.Element;
/** Owner model picker. `value` is `[platform:]provider/model`, or empty for the default. */
export declare function ModelPicker(props: PickerProps): JSX.Element;

// --- Tables and states ----------------------------------------------------

export type DataTableProps = ComponentPropsWithoutRef<'table'> & {
  /** Wrap the table in a bordered card that scrolls horizontally. */
  framed?: boolean;
};
export declare function DataTable(props: DataTableProps): JSX.Element;
export declare function EmptyState(props: ComponentPropsWithoutRef<'div'>): JSX.Element;
export declare function LoadingState(props: { children: ReactNode }): JSX.Element;
export declare function ErrorState(props: { title: string; description?: ReactNode; inline?: boolean; children?: ReactNode }): JSX.Element;

// --- Overlays and navigation ----------------------------------------------

export declare function Drawer(props: { title: string; onClose(): void; closeLabel?: string; children: ReactNode }): JSX.Element;

export type TabsProps = {
  value?: string;
  defaultValue?: string;
  onValueChange?(value: string): void;
  children: ReactNode;
};
export declare function Tabs(props: TabsProps): JSX.Element;
export declare function TabsList(props: { 'aria-label': string; children: ReactNode }): JSX.Element;
export declare function TabsTrigger(props: { value: string; disabled?: boolean; children: ReactNode }): JSX.Element;
export declare function TabsContent(props: { value: string; children: ReactNode }): JSX.Element;

// --- Display --------------------------------------------------------------

/** ocman's session status badge, e.g. `busy`, `waiting`, `done`, `error`. */
export declare function StatusBadge(props: { status: string; compact?: boolean }): JSX.Element;

export type Severity = 'critical' | 'high' | 'medium' | 'low' | 'info' | 'unknown';
/** The same pill as the page 1.4 `severity` display. */
export declare function SeverityPill(props: { severity: Severity }): JSX.Element;

export type PillTone = 'neutral' | 'accent' | 'success' | 'warning' | 'danger';
/** A small inline pill for plugin-defined states. */
export declare function Pill(props: { tone?: PillTone; children: ReactNode }): JSX.Element;

// --- Layout ---------------------------------------------------------------

export interface PageProps {
  title: string;
  description?: ReactNode;
  actions?: ReactNode;
  children?: ReactNode;
}
export declare function Page(props: PageProps): JSX.Element;
export declare function Section(props: PageProps): JSX.Element;
export declare function Properties(props: { items: { label: string; value: ReactNode }[] }): JSX.Element;
export declare function CodeBlock(props: { children: ReactNode; language?: string }): JSX.Element;
