import { chartResult } from '../src/test/a2uiFixture';
import { MOCK_SESSION, type Page } from './fixtures';

/** Actual inline chart renderer, fed only synthetic resource observations. */
export async function installInlineChartsDemo(page: Page, sessions: typeof MOCK_SESSION[]) {
  const session = { ...sessions[0], title: 'Compare cluster resources', status: 'done' };
  const now = Date.parse('2026-10-20T14:30:00Z');
  const result = chartResult();
  result.summary = 'Synthetic resource observations for a fictional application cluster.';
  const included = new Set(['root', 'stats', 'cpu', 'memory', 'errors', 'pies', 'cpuRequested', 'memoryRequested', 'cpuUsed', 'usage']);
  for (const message of result.messages) {
    if (!('updateComponents' in message)) continue;
    message.updateComponents.components = message.updateComponents.components.filter(component => included.has(component.id));
    const root = message.updateComponents.components.find(component => component.id === 'root');
    if (root?.component === 'Column') root.children = ['stats', 'pies', 'usage'];
    const usage = message.updateComponents.components.find(component => component.id === 'usage');
    if (usage?.component === 'LineChart') {
      usage.x = { type: 'date', values: ['2026-10-14', '2026-10-15', '2026-10-16', '2026-10-17', '2026-10-18', '2026-10-19', '2026-10-20'] };
      usage.series = [
        { name: 'api', values: [2.1, 2.5, 2.3, 2.8, 2.6, 3.0, 3.1] },
        { name: 'worker', values: [1.2, 1.6, 1.4, 1.9, 1.7, 2.0, 2.2] },
      ];
    }
  }
  await page.route('/api/sessions*', route => route.fulfill({ json: [session, ...sessions.slice(1)] }));
  await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({ json: {
    session, liveConnection: true, platform: 'opencode', defaultAgent: 'build', totalMessages: 2,
    messages: [
      { id: 'charts-user', sessionId: session.id, timeCreated: now - 120_000, data: { role: 'user' } },
      { id: 'charts-answer', sessionId: session.id, timeCreated: now - 60_000, data: {
        role: 'assistant', finish: 'stop', modelID: 'demo-model', providerID: 'demo',
        time: { created: now - 60_000, completed: now - 59_000 },
      } },
    ],
    parts: [
      { id: 'charts-prompt', messageId: 'charts-user', sessionId: session.id, data: { type: 'text',
        text: 'Compare CPU and memory across our demo apps. Show the breakdown and usage trend as charts here in the conversation.',
      } },
      { id: 'charts-result', messageId: 'charts-answer', sessionId: session.id, data: {
        type: 'tool', tool: 'ocman_render_ui', callID: 'demo-inline-charts', state: {
          status: 'completed', input: { action: 'render', intent: 'Compare resource usage with inline charts' },
          output: JSON.stringify({ ocmanA2ui: result }), time: { start: now - 60_000, end: now - 59_000 },
        },
      } },
    ],
  } }));
}
