export const formulaStages = [
  ['planning', 'Planning prompt'],
  ['scope_expansion', 'Scope expansion prompt'],
  ['implementation', 'Implementation prompt'],
  ['delivery', 'Delivery prompt'],
] as const;

export type FormulaStage = typeof formulaStages[number][0];

// Only top-level keys belong to the Formula, never keys inside graph tables.
export function setFormulaPrompt(source: string, stage: FormulaStage, text: string): string {
  const table = source.search(/^\s*\[\[/m);
  const header = table < 0 ? source : source.slice(0, table);
  const rest = table < 0 ? '' : source.slice(table);
  const key = `prompt_${stage}`;
  const line = `${key} = ${JSON.stringify(text)}`;
  const pattern = new RegExp(`^\\s*${key}\\s*=.*$`, 'm');
  return (pattern.test(header) ? header.replace(pattern, () => line) : `${line}\n${header}`) + rest;
}

export function getFormulaPrompt(source: string, stage: FormulaStage, fallback: string): string {
  const header = source.split(/^\s*\[\[/m, 1)[0];
  const value = header.match(new RegExp(`^\\s*prompt_${stage}\\s*=\\s*(.*)$`, 'm'))?.[1];
  if (value === undefined) return fallback;
  try { const parsed: unknown = JSON.parse(value); return typeof parsed === 'string' ? parsed : value; } catch { return value; }
}
