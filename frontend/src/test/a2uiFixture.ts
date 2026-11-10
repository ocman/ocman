import type { A2UIResult } from '../lib/a2ui';
import catalog from '../../../internal/agentui/catalog.json' with { type: 'json' };

export function sampleResult(): A2UIResult {
  return {
    title: 'Build investigation', summary: 'Two jobs failed.', messages: [
      { version: 'v0.9.1', createSurface: { surfaceId: 'build', catalogId: catalog.catalogId } },
      { version: 'v0.9.1', updateComponents: { surfaceId: 'build', components: [
        { id: 'root', component: 'Column', children: ['heading', 'row', 'divider', 'table', 'chart', 'job', 'enabled', 'button'] },
        { id: 'heading', component: 'Text', text: 'Failed jobs', variant: 'h2' },
        { id: 'row', component: 'Row', children: ['card'] },
        { id: 'card', component: 'Card', child: 'caption' },
        { id: 'caption', component: 'Text', text: { path: '/job' }, variant: 'caption' },
        { id: 'divider', component: 'Divider' },
        { id: 'table', component: 'Table', columns: ['Job', 'Failures'], rows: [['Frontend', 10], ['Backend', 2]] },
        { id: 'chart', component: 'BarChart', label: 'Failures by job', data: [{ label: 'Frontend', value: 10 }, { label: 'Backend', value: 2 }] },
        { id: 'job', component: 'TextField', label: 'Investigate job', value: { path: '/job' } },
        { id: 'enabled', component: 'CheckBox', label: 'Include logs', value: { path: '/logs' } },
        { id: 'button', component: 'Button', child: 'buttonLabel', action: { event: { name: 'investigate', context: { job: { path: '/job' }, logs: { path: '/logs' }, fixed: 'literal', missing: { path: '/missing' } } } } },
        { id: 'buttonLabel', component: 'Text', text: 'Continue investigation' },
      ] } },
      { version: 'v0.9.1', updateDataModel: { surfaceId: 'build', value: { job: 'Backend', logs: false } } },
    ],
  };
}

const apps = ['api', 'worker', 'web', 'cron'];
const pie = (id: string, label: string, values: number[], hint: { unit?: string; format?: 'bytes' } = {}) =>
  ({ id, component: 'PieChart' as const, label, ...hint, data: apps.map((app, i) => ({ label: app, value: values[i] })) });

/** Every chart component, mirroring agentui's chartExample plus the other chart types. */
export function chartResult(): A2UIResult {
  return {
    title: 'Cluster resources', summary: 'api uses most of the requested CPU.', messages: [
      { version: 'v0.9.1', createSurface: { surfaceId: 'resources', catalogId: catalog.catalogId } },
      { version: 'v0.9.1', updateComponents: { surfaceId: 'resources', components: [
        { id: 'root', component: 'Column', children: ['stats', 'pies', 'usage', 'bars', 'scatter'] },
        { id: 'stats', component: 'Row', children: ['cpu', 'memory', 'errors'] },
        { id: 'cpu', component: 'Stat', label: 'CPU used', value: 6.4, unit: 'cores', delta: 0.8, deltaLabel: 'vs last week', better: 'down' },
        { id: 'memory', component: 'Stat', label: 'Memory total', value: 25769803776, format: 'bytes' },
        { id: 'errors', component: 'Stat', label: 'Error rate', value: 1.5, format: 'percent', delta: -0.5, better: 'down' },
        { id: 'pies', component: 'Row', children: ['cpuRequested', 'memoryRequested', 'cpuUsed'] },
        pie('cpuRequested', 'CPU requested', [4, 2.5, 1, 0.5], { unit: 'cores' }),
        // Different order, same entities: colors must follow the name.
        { ...pie('memoryRequested', 'Memory requested', [12884901888, 8589934592, 3221225472, 1073741824], { format: 'bytes' }), data: [
          { label: 'cron', value: 1073741824 }, { label: 'web', value: 3221225472 }, { label: 'worker', value: 8589934592 }, { label: 'api', value: 12884901888 },
        ], variant: 'pie' },
        pie('cpuUsed', 'CPU used share', [3.1, 2.2, 0.7, 0.4], { unit: 'cores' }),
        { id: 'usage', component: 'LineChart', label: 'CPU used per day', unit: 'cores', area: true,
          x: { type: 'date', values: ['2026-11-01', '2026-11-02', '2026-11-03'] },
          series: [{ name: 'worker', values: [1.9, null, 2.2] }, { name: 'api', values: [2.8, 3.0, 3.1] }] },
        { id: 'bars', component: 'MultiBarChart', label: 'CPU by app', mode: 'stacked', orientation: 'horizontal', unit: 'cores', xLabel: 'App',
          categories: apps, series: [{ name: 'requested', values: [4, 2.5, 1, null] }, { name: 'used', values: [3.1, 2.2, 0.7, 0.4] }] },
        { id: 'scatter', component: 'ScatterChart', label: 'Requested vs used', xLabel: 'Requested', yLabel: 'Used', xUnit: 'cores', unit: 'cores',
          series: [{ name: 'api', points: [[4, 3.1]] }, { name: 'web', points: [[1, 0.7], [0.5, 0.4]] }] },
      ] } },
    ],
  };
}
