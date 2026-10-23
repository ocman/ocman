import { ESLint } from 'eslint';
import { describe, expect, it } from 'vitest';

const eslint = new ESLint();

async function restrictions(code: string, filePath = 'src/lint-probe.tsx') {
  const [result] = await eslint.lintText(code, { filePath });
  expect(result.fatalErrorCount).toBe(0);
  return result.messages.filter(message => message.ruleId === 'no-restricted-syntax');
}

describe('architecture restrictions', () => {
  it.each([
    "export const show = session.platform === 'claude';",
    "export const show = session.platform !== 'opencode';",
    "export const show = session?.platform === 'claude';",
    "export const View = () => <div className='settings-row' />;",
    "export const View = () => <div className='settings-row extra' />;",
    "export const View = () => <button />;",
  ])('rejects %s', async code => {
    expect(await restrictions(code)).toHaveLength(1);
  });

  it.each([
    "export const show = typeof session.platform === 'string';",
    "export const show = session.platform === selectedPlatform;",
    "export const show = session.platform === other.platform;",
    "export const View = () => <div className='settings-row-label' />;",
    "export const View = () => <SettingRow />;",
  ])('allows %s', async code => {
    expect(await restrictions(code)).toHaveLength(0);
  });

  it('exempts tests and the shared SettingRow implementation', async () => {
    expect(await restrictions("export const show = session.platform === 'claude';", 'src/probe.test.tsx')).toHaveLength(0);
    expect(await restrictions("export const View = () => <div className='settings-row' />;", 'src/components/SettingRow.tsx')).toHaveLength(0);
  });

  it('honors standard ESLint suppression comments', async () => {
    expect(await restrictions("// eslint-disable-next-line no-restricted-syntax -- test exception\nexport const show = session.platform === 'claude';")).toHaveLength(0);
  });
});
