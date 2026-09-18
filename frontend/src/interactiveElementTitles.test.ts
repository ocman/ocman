// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync, readdirSync } from 'node:fs';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { join } from 'node:path';
import ts from 'typescript';
import { expect, it } from 'vitest';

const controls = new Set(['a', 'button', 'Button', 'Link', 'NavLink']);
const labels = new Set(['aria-label', 'aria-labelledby', 'title']);

function tsxFiles(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((entry: { isDirectory(): boolean; name: string }) => {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) return tsxFiles(path);
    return entry.name.endsWith('.tsx') && !entry.name.includes('.test.') ? [path] : [];
  });
}

function expressionText(expression: ts.Expression): string | undefined {
  if (ts.isStringLiteral(expression) || ts.isNoSubstitutionTemplateLiteral(expression)) return expression.text;
  if (ts.isParenthesizedExpression(expression)) return expressionText(expression.expression);
  if (ts.isConditionalExpression(expression)) {
    return `${expressionText(expression.whenTrue) ?? 'content'}${expressionText(expression.whenFalse) ?? 'content'}`;
  }
}

function staticText(node: ts.JsxElement): string {
  if (node.openingElement.tagName.getText() === 'svg') return '';
  return node.children.map((child) => {
    if (ts.isJsxText(child)) return child.text;
    if (ts.isJsxElement(child)) return staticText(child);
    if (ts.isJsxSelfClosingElement(child)) {
      const tag = child.tagName.getText();
      return tag === 'i' || tag === 'svg' || tag.endsWith('Icon') ? '' : 'content';
    }
    if (ts.isJsxExpression(child) && child.expression) return expressionText(child.expression) ?? 'content';
    return 'content';
  }).join('').trim();
}

function hasLabel(attribute: ts.JsxAttributeLike): boolean {
  if (!ts.isJsxAttribute(attribute) || !labels.has(attribute.name.getText()) || !attribute.initializer) return false;
  if (ts.isStringLiteral(attribute.initializer)) return Boolean(attribute.initializer.text.trim());
  if (ts.isJsxExpression(attribute.initializer) && attribute.initializer.expression) {
    return Boolean((expressionText(attribute.initializer.expression) ?? 'content').trim());
  }
  return true;
}

it('gives symbol-only links and buttons an explicit accessible name', () => {
  const missing: string[] = [];
  for (const file of tsxFiles('src')) {
    const source = ts.createSourceFile(file, readFileSync(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.TSX);
    const visit = (node: ts.Node) => {
      if (ts.isJsxElement(node) && controls.has(node.openingElement.tagName.getText(source))) {
        const text = staticText(node);
        const named = node.openingElement.attributes.properties.some(hasLabel);
        if (!named && !/[\p{L}\p{N}]/u.test(text)) {
          const { line } = source.getLineAndCharacterOfPosition(node.getStart(source));
          missing.push(`${file}:${line + 1}`);
        }
      }
      ts.forEachChild(node, visit);
    };
    visit(source);
  }
  expect(missing).toEqual([]);
});
