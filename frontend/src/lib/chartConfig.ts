import { formatCompactNumber, formatCurrency } from './format';

/**
 * Standard tick configuration for time-series x-axes used by the
 * dashboard's bar / line charts. Limits the number of labels to ten
 * and rotates them 45° to avoid overlap on dense buckets.
 */
export const CHART_X_TICKS = { maxTicksLimit: 10, maxRotation: 45, minRotation: 45 };

/**
 * Categorical palette used by the doughnut and stacked charts. Hand-
 * picked to read well on the dark theme; colours alternate so
 * adjacent slices in a doughnut don't blur together.
 */
export const CHART_COLORS = [
  '#89b4fa', '#a6e3a1', '#cba6f7', '#fab387',
  '#f38ba8', '#74c7ec', '#94e2d5', '#f9e2af',
];

/** Palette used for the stacked stop-reason breakdown. */
export const STOP_REASON_COLORS = [
  '#f38ba8', '#a6e3a1', '#f9e2af', '#89b4fa',
  '#cba6f7', '#fab387', '#94e2d5',
];

const baseLegendLabels = { color: '#bac2de', boxWidth: 12, padding: 12 } as const;

/**
 * Tokens-per-second bar chart. Y-axis ticks are suffixed with
 * `Tok/s` so the unit is visible without a chart title.
 */
export const BAR_OPTIONS_TOKS = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: { beginAtZero: true, ticks: { callback: (v: string | number) => `${v}Tok/s` } },
  },
} as const;

/** Duration bar chart with seconds suffix on the y-axis. */
export const BAR_OPTIONS_DURATION = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: { beginAtZero: true, ticks: { callback: (v: string | number) => `${v}s` } },
  },
} as const;

/** Stacked bar chart using the compact-number formatter for values. */
export const BAR_OPTIONS_STACKED = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { position: 'top' as const, labels: baseLegendLabels } },
  scales: {
    x: { stacked: true, grid: { display: false }, ticks: CHART_X_TICKS },
    y: {
      stacked: true,
      beginAtZero: true,
      ticks: { callback: (v: string | number) => formatCompactNumber(Number(v)) },
    },
  },
} as const;

/** Cumulative cost line chart with currency formatting. */
export const LINE_OPTIONS_COST = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: { beginAtZero: true, ticks: { callback: (v: string | number) => formatCurrency(Number(v), 2) } },
  },
} as const;

/**
 * Stacked-area cumulative cost chart split by model. Tooltip shows the
 * per-model contribution as currency and the legend names each model
 * stack.
 */
export const LINE_OPTIONS_COST_STACKED = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  interaction: { mode: 'index' as const, intersect: false },
  plugins: {
    legend: { position: 'bottom' as const, labels: { ...baseLegendLabels, padding: 8, font: { size: 11 } } },
    tooltip: {
      callbacks: {
        label: (ctx: { dataset: { label?: string }; parsed: { y: number | null } }) =>
          `${ctx.dataset.label ?? ''}: ${formatCurrency(Number(ctx.parsed.y ?? 0), 2)}`,
      },
    },
  },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: {
      stacked: true,
      beginAtZero: true,
      ticks: { callback: (v: string | number) => formatCurrency(Number(v), 2) },
    },
  },
} as const;

/** Cache-efficiency line chart capped at 100 % with `%` suffix. */
export const LINE_OPTIONS_CACHE = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false }, ticks: CHART_X_TICKS },
    y: {
      beginAtZero: true,
      max: 100,
      ticks: { callback: (v: string | number) => `${Number(v).toFixed(0)}%` },
    },
  },
} as const;

/** Doughnut chart with a right-side legend (stop-reason breakdown). */
export const DOUGHNUT_OPTIONS = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  cutout: '62%',
  plugins: { legend: { position: 'right' as const, labels: baseLegendLabels } },
} as const;

/**
 * Sessions-per-day bar chart. The custom tick callback only renders
 * the first / last / every-Nth label so a long range stays readable.
 */
export const BAR_OPTIONS_SESSIONS = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: {
    legend: {
      position: 'top' as const,
      labels: { color: '#bac2de', boxWidth: 12, padding: 8, font: { size: 11 } },
    },
  },
  scales: {
    x: {
      grid: { display: false },
      ticks: {
        maxTicksLimit: 12,
        callback: (_: unknown, idx: number, ticks: unknown[]) =>
          idx === 0 || idx === ticks.length - 1 || idx % Math.ceil(ticks.length / 12) === 0
            ? undefined
            : null,
      },
    },
    y: { beginAtZero: true },
  },
} as const;

/** Hour-of-day bar chart (no legend, default ticks). */
export const BAR_OPTIONS_HOURLY = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: { legend: { display: false } },
  scales: {
    x: { grid: { display: false } },
    y: { beginAtZero: true },
  },
} as const;

/** Horizontal stacked bars for tokens-by-model breakdown. */
export const BAR_OPTIONS_TOKENS_BY_MODEL = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  indexAxis: 'y' as const,
  plugins: {
    legend: {
      position: 'bottom' as const,
      labels: { color: '#bac2de', boxWidth: 12, padding: 8, font: { size: 11 } },
    },
  },
  scales: {
    x: {
      beginAtZero: true,
      stacked: true,
      ticks: { callback: (v: string | number) => formatCompactNumber(Number(v)) },
    },
    y: { grid: { display: false }, stacked: true },
  },
} as const;

/** Stacked hourly tokens chart (24 buckets, compact-number labels). */
export const BAR_OPTIONS_HOURLY_TOKENS = {
  responsive: true,
  maintainAspectRatio: false,
  animation: false as const,
  plugins: {
    legend: {
      position: 'bottom' as const,
      labels: { color: '#bac2de', boxWidth: 12, padding: 8, font: { size: 11 } },
    },
  },
  scales: {
    x: { stacked: true, grid: { display: false }, ticks: { maxRotation: 0, autoSkip: false } },
    y: {
      stacked: true,
      beginAtZero: true,
      ticks: { callback: (v: string | number) => formatCompactNumber(Number(v)) },
    },
  },
} as const;
