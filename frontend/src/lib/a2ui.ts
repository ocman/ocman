import validate from './a2uiValidator.generated.js';
import { mcpToolLabel } from './mcpToolLabel';
import { parseToolTime } from './threadHelpers';

export type Scalar = string | number | boolean | null;
export type Binding = { path: string };
export type DynamicString = string | Binding;
export type Component =
  | { id: string; component: 'Text'; text: DynamicString; variant?: 'body' | 'h2' | 'h3' | 'caption' }
  | { id: string; component: 'Column' | 'Row'; children: string[] }
  | { id: string; component: 'Card'; child: string }
  | { id: string; component: 'Divider' }
  | { id: string; component: 'TextField' | 'CheckBox'; label: string; value: Binding }
  | { id: string; component: 'Button'; child: string; action: { event: { name: string; context?: Record<string, Scalar | Binding> } } }
  | { id: string; component: 'Table'; columns: string[]; rows: Scalar[][] }
  | ({ id: string; component: 'BarChart'; label: string; data: { label: string; value: number }[] } & ValueHint)
  | ({ id: string; component: 'PieChart'; label: string; variant?: 'donut' | 'pie'; data: { label: string; value: number }[] } & ValueHint)
  | ({ id: string; component: 'MultiBarChart'; label: string; mode?: 'grouped' | 'stacked'; orientation?: 'vertical' | 'horizontal';
    categories: string[]; series: Series[] } & AxisLabels & ValueHint)
  | ({ id: string; component: 'LineChart'; label: string; area?: boolean; series: Series[];
    x: { type: 'category' | 'date'; values: string[] } | { type: 'number'; values: number[] } } & AxisLabels & ValueHint)
  | ({ id: string; component: 'ScatterChart'; label: string; xUnit?: string; series: { name: string; points: [number, number][] }[] } & AxisLabels & ValueHint)
  | ({ id: string; component: 'Stat'; label: string; value: number; delta?: number; deltaLabel?: string; better?: 'up' | 'down' | 'neither' } & ValueHint);

export type ValueFormat = 'number' | 'compact' | 'percent' | 'bytes';
export type ValueHint = { unit?: string; format?: ValueFormat };
type AxisLabels = { xLabel?: string; yLabel?: string };
export type Series = { name: string; values: (number | null)[] };
export type ChartComponent = Extract<Component, { component: 'PieChart' | 'MultiBarChart' | 'LineChart' | 'ScatterChart' }>;
/** Canvas charts per rendered result; mirrors agentui.MaxCharts. */
export const MAX_CHARTS = 12;
export function isCanvasChart(c: Component): c is ChartComponent {
  return c.component === 'PieChart' || c.component === 'MultiBarChart' || c.component === 'LineChart' || c.component === 'ScatterChart';
}

type Message = { version: 'v0.9.1' } & (
  | { createSurface: { surfaceId: string; catalogId: string } }
  | { updateComponents: { surfaceId: string; components: Component[] } }
  | { updateDataModel: { surfaceId: string; value: Record<string, Scalar>; path?: '/' } }
);
export type A2UIResult = { title: string; summary: string; messages: Message[] };
export type Surface = { id: string; components: Map<string, Component>; data: Record<string, Scalar> };

export function buildSurface(value: unknown): Surface | null {
  if (!validate(value)) return null;
  let id = '';
  const components = new Map<string, Component>();
  let data: Record<string, Scalar> = {};
  for (const message of value.messages) {
    if ('createSurface' in message) {
      if (id) return null;
      id = message.createSurface.surfaceId;
    } else {
      const update = 'updateComponents' in message ? message.updateComponents : message.updateDataModel;
      if (!id || id !== update.surfaceId) return null;
      if ('components' in update) {
        if (!update.components.every(validChart)) return null;
        for (const c of update.components) components.set(c.id, c);
      } else data = update.value;
    }
  }
  if (!components.has('root') || components.size > 100) return null;
  let visits = 0, charts = 0;
  function walk(key: string, ancestors: Set<string>, depth: number): boolean {
    visits++;
    if (depth > 16 || visits > 500 || ancestors.has(key)) return false;
    const c = components.get(key);
    if (!c) return false;
    if (isCanvasChart(c) && ++charts > MAX_CHARTS) return false;
    const next = new Set(ancestors).add(key);
    if ('child' in c) {
      if (!walk(c.child, next, depth + 1)) return false;
      if (c.component === 'Button' && components.get(c.child)?.component !== 'Text') return false;
    }
    return !('children' in c) || c.children.every(child => walk(child, next, depth + 1));
  }
  for (const key of components.keys()) {
    visits = 0;
    charts = 0;
    if (!walk(key, new Set(), 0)) return null;
  }
  return { id, components, data };
}

/** Cross-field chart rules JSON Schema cannot express; mirrors agentui.validateChart. */
function validChart(c: Component): boolean {
  const unique = (names: string[]) => new Set(names).size === names.length;
  const lengths = (series: Series[], length: number) => series.every(s => s.values.length === length);
  switch (c.component) {
    case 'PieChart': return unique(c.data.map(item => item.label));
    case 'MultiBarChart': return unique(c.series.map(s => s.name)) && lengths(c.series, c.categories.length);
    case 'ScatterChart': return unique(c.series.map(s => s.name));
    case 'LineChart': {
      if (!unique(c.series.map(s => s.name)) || !lengths(c.series, c.x.values.length)) return false;
      if (c.x.type === 'category') return true;
      const points = c.x.type === 'date' ? c.x.values.map(parseChartDate) : c.x.values;
      return points.every((point, i) => !Number.isNaN(point) && (i === 0 || point > points[i - 1]));
    }
    default: return true;
  }
}

/** Milliseconds for the catalog's ISO 8601 subset, or NaN. Times without a zone are UTC,
 * and impossible calendar dates are rejected as Go's time.Parse does. */
export function parseChartDate(value: string): number {
  const match = /^(\d{4})-(\d{2})-(\d{2})(?:T(\d{2}):(\d{2})(?::(\d{2})(?:\.\d{1,3})?)?(Z|[+-]\d{2}:\d{2})?)?$/.exec(value);
  if (!match) return NaN;
  const [, year, month, day, hour, minute, second] = match.map(Number);
  if (month < 1 || month > 12 || day < 1 || day > new Date(Date.UTC(year, month, 0)).getUTCDate()) return NaN;
  if (match[4] && (hour > 23 || minute > 59 || second > 59)) return NaN;
  return Date.parse(match[4] && !match[7] ? `${value}Z` : value);
}

function object(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

/** Accept only a dedicated tool call, or a literal Code Mode call to that tool.
 * All output is untrusted and revalidated, including retained/shared history. */
export function parseA2UIResult(toolName: string, argsText: string, result: unknown): A2UIResult | null {
  argsText = parseToolTime(argsText)?.strippedArgs ?? argsText;
  if (argsText.split('\n')[0] !== 'completed') return null;
  const direct = /^(?:mcp_+)?ocman_+render_ui$/.test(toolName);
  const codeMode = mcpToolLabel(toolName, argsText.slice(argsText.indexOf('\n') + 1)) === 'MCP ocman.render_ui';
  if (!direct && !codeMode) return null;
  try {
    const raw = typeof result === 'string' ? result : JSON.stringify(result);
    if (!raw || raw.length > 256 * 1024) return null;
    let parsed: unknown = JSON.parse(raw);
    // MCP clients may retain the CallToolResult envelope or just its text.
    if (object(parsed) && Array.isArray(parsed.content)) {
      if (parsed.isError || parsed.content.length !== 1) return null;
      const text = parsed.content[0];
      if (!object(text) || text.type !== 'text' || typeof text.text !== 'string') return null;
      parsed = JSON.parse(text.text);
    }
    if (!object(parsed) || !object(parsed.ocmanA2ui)) return null;
    const ui = parsed.ocmanA2ui;
    if (new TextEncoder().encode(JSON.stringify(ui)).length > 64 * 1024 || !buildSurface(ui)) return null;
    return ui as A2UIResult;
  } catch { return null; }
}

export function resolveValue(value: Scalar | Binding | undefined, data: Record<string, Scalar>): Scalar | undefined {
  return typeof value === 'object' && value !== null
    ? (Object.hasOwn(data, value.path.slice(1)) ? data[value.path.slice(1)] : undefined)
    : value;
}

export type A2UIAction = { action: { name: string; surfaceId: string; sourceComponentId: string; timestamp: string; context: Record<string, Scalar> } };
