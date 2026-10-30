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
