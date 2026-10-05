#!/usr/bin/env node
// Markdown before/after table for `pnpm perf` result files.
//   node perf/compare.mjs ../tmp/perf/base.json ../tmp/perf/after.json
// Each argument may be a comma-separated list of runs; every cell is then
// the median across those runs (use interleaved A/B runs on a busy box).
import { readFileSync } from 'node:fs';

const args = process.argv.slice(2);
if (args.length === 0) {
  console.error('usage: compare.mjs <base.json[,base2.json…]> [after.json[,…]]');
  process.exit(1);
}
const median = (xs) => {
  const s = xs.filter((x) => x !== undefined).sort((a, b) => a - b);
  return s.length ? s[Math.floor((s.length - 1) / 2)] : undefined;
};
const runs = args.map((arg) => {
  const group = arg.split(',').map((f) => JSON.parse(readFileSync(f, 'utf8')));
  const get = (scenario, metric, series, stat) =>
    median(group.map((r) => r.results[scenario]?.metrics?.[metric]?.[series]?.[stat]));
  return { label: group[0].label.replace(/\d+$/, '') + (group.length > 1 ? ` (median of ${group.length})` : ''), results: group[0].results, get };
});

// [label, metric, series label, stat]
const ROWS = [
  ['interaction p50 (ms)', 'interaction', 'all', 'p50'],
  ['interaction p95 (ms)', 'interaction', 'all', 'p95'],
  ['long frames (count)', 'loaf', 'duration', 'count'],
  ['long frame p95 (ms)', 'loaf', 'duration', 'p95'],
  ['total blocking (ms)', 'loaf', 'blocking', 'sum'],
  ['SSE reduce p95 (ms)', 'sse.batch', 'reduce', 'p95'],
  ['SSE flush→commit p50 (ms)', 'sse.commit', 'flush→commit', 'p50'],
  ['SSE flush→commit p95 (ms)', 'sse.commit', 'flush→commit', 'p95'],
  ['SSE batches (count)', 'sse.batch', 'reduce', 'count'],
  ['Thread commit p50 (ms)', 'render', 'Thread', 'p50'],
  ['Thread commit p95 (ms)', 'render', 'Thread', 'p95'],
  ['Thread commit total (ms)', 'render', 'Thread', 'sum'],
  ['Sidebar commit total (ms)', 'render', 'SessionSidebar', 'sum'],
  ['RightPanel commit total (ms)', 'render', 'RightPanel', 'sum'],
  ['Page commit total (ms)', 'render', 'SessionPage', 'sum'],
  ['switch→paint, cache hit p50 (ms)', 'switch', 'hit', 'p50'],
  ['switch→paint, cache hit p95 (ms)', 'switch', 'hit', 'p95'],
  ['switch→paint, cache miss p50 (ms)', 'switch', 'miss', 'p50'],
  ['switch→paint, cache miss p95 (ms)', 'switch', 'miss', 'p95'],
];

const fmt = (v) => (v === undefined ? '–' : String(Math.round(v * 10) / 10));
const head = ['scenario', 'metric', ...runs.map((r) => r.label), ...(runs.length === 2 ? ['Δ'] : [])];
console.log(`| ${head.join(' | ')} |`);
console.log(`|${head.map(() => ' --- ').join('|')}|`);
for (const scenario of Object.keys(runs[0].results)) {
  for (const [label, metric, series, stat] of ROWS) {
    const vals = runs.map((r) => r.get(scenario, metric, series, stat));
    if (vals.every((v) => v === undefined)) continue;
    const cells = [scenario, label, ...vals.map(fmt)];
    if (runs.length === 2) {
      const [a, b] = vals;
      cells.push(a && b !== undefined ? `${b <= a ? '' : '+'}${Math.round(((b - a) / a) * 100)}%` : '–');
    }
    console.log(`| ${cells.join(' | ')} |`);
  }
}
