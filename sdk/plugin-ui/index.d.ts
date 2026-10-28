// @ocman/plugin-ui 1.1.0: the public, semver-versioned surface a plugin UI
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

/**
 * Sets the app header title for the current plugin view (shown after the
 * plugin name) and the document title. Since 1.1.0.
 */
export declare function usePageTitle(title: string): void;

export interface ConfirmOptions {
  /** Names the item, e.g. "Delete item Desk lamp?". */
  title: string;
  /** Known consequences. */
  description?: ReactNode;
  /** Names the action, e.g. "Delete item", never "OK". */
  confirmLabel: string;
  cancelLabel?: string;
  /** Destructive actions use danger styling and initially focus Cancel. */
  destructive?: boolean;
}

/** ocman's confirmation dialog; resolves true when confirmed. Since 1.1.0. */
export declare function useConfirm(): (options: ConfirmOptions) => Promise<boolean>;

/**
 * While `dirty`, asks before leaving through any in-app link (including main
 * navigation) and before the page unloads. Browser Back is not intercepted.
 * `confirmDiscard` asks the same question for the plugin's own Cancel/Close.
 * Since 1.1.0.
 */
export declare function useUnsavedChangesGuard(dirty: boolean): { confirmDiscard(): Promise<boolean> };

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
/** Grouped browse-and-open rows that stack on phones. Since 1.1.0. */
export declare function DataTableGroup(props: { label: string; noun: string; count: number; children: ReactNode }): JSX.Element;
/** One stacked row: a title link or button, a secondary line and end-aligned metadata or actions. Since 1.1.0. */
export declare function DataTableRow(props: { primary: ReactNode; secondary?: ReactNode; meta?: ReactNode }): JSX.Element;
/** Placeholder rows for initial table loading, with the real column headers. Since 1.1.0. */
export declare function SkeletonTable(props: { columns: string[]; rows?: number; label: string }): JSX.Element;
/** Placeholder text lines for initial loading of lists without columns. Since 1.1.0. */
export declare function SkeletonLines(props: { rows?: number; label: string }): JSX.Element;
/**
 * Dedicated empty or unavailable state. `icon` names an icon (see `Icon`);
 * defaults depend on `error`. Put Retry or creation actions in children.
 * Since 1.1.0.
 */
export declare function PageState(props: { title: string; description: ReactNode; icon?: string; error?: boolean; children?: ReactNode }): JSX.Element;
/** Compact warning that keeps content visible, e.g. a failed background refresh. Since 1.1.0. */
export declare function InlineAlert(props: { children: ReactNode; onRetry?(): void; retrying?: boolean; compact?: boolean }): JSX.Element;
export declare function EmptyState(props: ComponentPropsWithoutRef<'div'>): JSX.Element;
export declare function LoadingState(props: { children: ReactNode }): JSX.Element;
export declare function ErrorState(props: { title: string; description?: ReactNode; inline?: boolean; children?: ReactNode }): JSX.Element;

// --- Overlays and navigation ----------------------------------------------

/**
 * Route views styled as tabs. They remain links with aria-current, and
 * switching keeps the current search parameters. A `to` without a leading
 * `/` is relative to the plugin root (`""` is the index; give it `end`).
 * Since 1.1.0.
 */
export declare function RouteTabs(props: { label: string; tabs: { to: string; label: string; end?: boolean }[] }): JSX.Element;

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

/**
 * An icon from the Bootstrap Icons 1.x set ocman ships, by name without the
 * `bi-` prefix, e.g. "inbox". Decorative unless `label` is given. Since 1.1.0.
 */
export declare function Icon(props: { name: string; label?: string }): JSX.Element | null;

/** Relative time that keeps counting, with the exact date on hover and focus. Since 1.1.0. */
export declare function RelativeTime(props: { value: string | number | Date }): JSX.Element | null;
/** ocman's exact date for records, e.g. "Oct 28, 21:32"; the year only when it differs. Since 1.1.0. */
export declare function formatExactDate(value: string | number | Date): string;

// --- Layout ---------------------------------------------------------------

/**
 * `Page` sets `title` as the app header title (see `usePageTitle`) instead of
 * rendering a second heading; since 1.1.0. `Section` renders `title` as h2.
 */
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
