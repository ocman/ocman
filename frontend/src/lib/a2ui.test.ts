import { describe, expect, it } from 'vitest';
import { buildSurface, parseA2UIResult, resolveValue } from './a2ui';
import { sampleResult } from '../test/a2uiFixture';

describe('A2UI validation', () => {
  it('accepts the supported catalog and resolves scalar bindings', () => {
    const ui = sampleResult();
    expect(buildSurface(ui)?.components.size).toBe(12);
    expect(resolveValue({ path: '/job' }, { job: 'Backend' })).toBe('Backend');
    expect(resolveValue('text', {})).toBe('text');
    expect(resolveValue(null, {})).toBeNull();
    expect(resolveValue({ path: '/constructor' }, {})).toBeUndefined();
  });

  it.each(['ocman_render_ui', 'mcp_ocman_render_ui', 'mcp__ocman__render_ui'])('recognizes completed %s calls', toolName => {
    expect(parseA2UIResult(toolName, 'completed\n{}', JSON.stringify({ ocmanA2ui: sampleResult() }))?.title).toBe('Build investigation');
  });

  it('recognizes literal Code Mode calls and MCP result envelopes', () => {
    const envelope = { content: [{ type: 'text', text: JSON.stringify({ ocmanA2ui: sampleResult() }) }] };
    const args = `completed\n${JSON.stringify({ code: 'return await tools.ocman.render_ui({action: "render"});' })}`;
    expect(parseA2UIResult('execute', args, envelope)?.title).toBe('Build investigation');
    expect(parseA2UIResult('execute', args, { ...envelope, isError: true })).toBeNull();
    expect(parseA2UIResult('execute', args, { content: [{ type: 'image' }] })).toBeNull();
  });

  it('recognizes Code Mode calls with conversation timing metadata', () => {
    const args = `completed\n@time:2000,2001\n${JSON.stringify({ code: 'return await tools.ocman.render_ui({action: "render"});' })}`;
    expect(parseA2UIResult('execute', args, { ocmanA2ui: sampleResult() })?.title).toBe('Build investigation');
  });

  it('rejects unrelated tools, nonterminal calls, malformed and oversized output', () => {
    const output = JSON.stringify({ ocmanA2ui: sampleResult() });
    expect(parseA2UIResult('bash', 'completed\n{}', output)).toBeNull();
    expect(parseA2UIResult('ocman_render_ui', 'running\n{}', output)).toBeNull();
    expect(parseA2UIResult('ocman_render_ui', 'error\n{}', output)).toBeNull();
    expect(parseA2UIResult('ocman_render_ui', 'completed\n{}', '{')).toBeNull();
    expect(parseA2UIResult('ocman_render_ui', 'completed\n{}', null)).toBeNull();
    expect(parseA2UIResult('ocman_render_ui', 'completed\n{}', { ocmanA2ui: {} })).toBeNull();
    expect(parseA2UIResult('ocman_render_ui', 'completed\n{}', 'x'.repeat(300_000))).toBeNull();
  });

  it('bounds shared subtrees, nesting depth, and aggregate components across messages', () => {
    const value = mutable();
    const components = [{ id: 'root', component: 'Column', children: ['left0', 'right0'] }];
    for (let i = 0; i < 10; i++) {
      for (const side of ['left', 'right']) {
        components.push(i < 9
          ? { id: `${side}${i}`, component: 'Column', children: [`left${i + 1}`, `right${i + 1}`] }
          : { id: `${side}${i}`, component: 'Column', children: [] });
      }
    }
    value.messages[1].updateComponents.components = components;
    expect(buildSurface(value)).toBeNull();
    value.messages[1].updateComponents.components = Array.from({ length: 18 }, (_, i) => ({
      id: i === 0 ? 'root' : `node${i}`, component: 'Column', children: i < 17 ? [`node${i + 1}`] : [],
    }));
    expect(buildSurface(value)).toBeNull();
    value.messages[1].updateComponents.components = Array.from({ length: 100 }, (_, i) => ({
      id: i === 0 ? 'root' : `node${i}`, component: 'Text', text: 'Value',
    }));
    expect(buildSurface(value)).not.toBeNull();
    value.messages.push({ version: 'v0.9.1', updateComponents: { surfaceId: 'build', components: [{ id: 'overflow', component: 'Text', text: 'Value' }] } });
    expect(buildSurface(value)).toBeNull();
  });

  it.each([
    ['wrong catalog', (v: ReturnType<typeof mutable>) => { v.messages[0].createSurface.catalogId = 'other'; }],
    ['wrong version', (v: ReturnType<typeof mutable>) => { v.messages[0].version = 'v0.8'; }],
    ['multiple operations', (v: ReturnType<typeof mutable>) => { v.messages[0].updateComponents = v.messages[1].updateComponents; }],
    ['missing root', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[0].id = 'absent'; }],
    ['missing child', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[0].children = ['absent']; }],
    ['cycle', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[0].children = ['root']; }],
    ['wrong surface', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.surfaceId = 'other'; }],
    ['duplicate surface', (v: ReturnType<typeof mutable>) => { v.messages.push(v.messages[0]); }],
    ['update before create', (v: ReturnType<typeof mutable>) => { v.messages.reverse(); }],
    ['unsupported component', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[0].component = 'HTML'; }],
    ['unknown props', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[1].html = '<script/>'; }],
    ['nested binding', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[8].value.path = '/nested/key'; }],
    ['prototype binding', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[8].value.path = '/constructor'; }],
    ['button control child', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[10].child = 'job'; }],
    ['negative chart', (v: ReturnType<typeof mutable>) => { v.messages[1].updateComponents.components[7].data[0].value = -1; }],
  ])('rejects %s', (_, mutate) => {
    const value = mutable();
    mutate(value);
    expect(buildSurface(value)).toBeNull();
  });
});

// Invalid data is intentional in validation tests.
// eslint-disable-next-line @typescript-eslint/no-explicit-any
function mutable(): any { return structuredClone(sampleResult()); }
