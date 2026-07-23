import { readFileSync } from 'node:fs';
import { expect, test } from '@playwright/test';

const css = readFileSync(new URL('../src/components/RightPanel.css', import.meta.url), 'utf8');

type TicketOptions = {
  depth?: number;
  isLast?: boolean;
  ancestorContinues?: boolean[];
  children?: string;
};

const ticket = (
  id: string,
  { depth = 0, isLast = true, ancestorContinues = [], children = '' }: TicketOptions = {},
) => {
  const trunk = depth * 12;
  return `
    <li data-testid="ticket-${id}">
      <div class="oc-beads-ticket" data-testid="ticket-row-${id}" style="--oc-beads-marker-width:${trunk + 17}px">
        <span class="oc-beads-marker" data-testid="marker-${id}">
          ${ancestorContinues.map((continues, level) => continues ? `<span class="oc-beads-guide" style="left:${level * 12}px"></span>` : '').join('')}
          <span class="oc-beads-branch${isLast ? '' : ' continues'}" data-testid="branch-${id}" style="left:${trunk}px"></span>
          ${children ? `<span class="oc-beads-child-bridge" data-testid="child-bridge-${id}" style="left:${trunk + 12}px"></span>` : ''}
          <span class="oc-beads-status open" data-testid="status-${id}" style="left:${trunk + 7}px" aria-label="open"></span>
        </span>
        <div class="oc-beads-content">
          <div class="oc-beads-main"><span>P1</span><span class="oc-beads-title">Ticket ${id}</span></div>
          <div class="oc-beads-details"><span>[task]</span><code>${id}</code></div>
        </div>
      </div>
      ${children}
    </li>`;
};

test('tree connectors meet circle borders and stop at final branches', async ({ page }) => {
  const children = `<ul>${ticket('first', { depth: 1, isLast: false, ancestorContinues: [true] })}${ticket('last', { depth: 1, ancestorContinues: [true] })}</ul>`;
  await page.setContent(`
    <style>:root { --border: #666; --accent2: #0f0; --text-dim: #999; } ${css}</style>
    <div class="oc-beads-pane" data-testid="beads-pane">
      <ul class="oc-beads-tree" aria-label="Beads tickets">
        ${ticket('parent', { isLast: false, children })}
        ${ticket('final-root')}
      </ul>
    </div>`);

  const geometry = await page.evaluate(() => {
    const measure = (id: string) => {
      const circle = document.querySelector<HTMLElement>(`[data-testid="status-${id}"]`)!;
      const branch = document.querySelector<HTMLElement>(`[data-testid="branch-${id}"]`)!;
      const branchRect = branch.getBoundingClientRect();
      const circleRect = circle.getBoundingClientRect();
      const elbow = getComputedStyle(branch, '::after');
      return {
        horizontalEnd: branchRect.left + parseFloat(elbow.width),
        horizontalY: branchRect.top + parseFloat(elbow.top),
        circleLeft: circleRect.left,
        circleCenterY: circleRect.top + circleRect.height / 2,
        trunkX: branchRect.left,
        continues: branch.classList.contains('continues'),
      };
    };
    const circleRect = document.querySelector<HTMLElement>('[data-testid="status-parent"]')!.getBoundingClientRect();
    const bridgeRect = document.querySelector<HTMLElement>('[data-testid="child-bridge-parent"]')!.getBoundingClientRect();
    return {
      fontSize: getComputedStyle(document.querySelector<HTMLElement>('[data-testid="beads-pane"]')!).fontSize,
      parent: measure('parent'),
      first: measure('first'),
      last: measure('last'),
      finalRoot: measure('final-root'),
      bridgeX: bridgeRect.left,
      bridgeY: bridgeRect.top,
      parentCircleCenterX: circleRect.left + circleRect.width / 2,
      parentCircleBottom: circleRect.bottom,
    };
  });

  for (const row of [geometry.parent, geometry.first, geometry.last, geometry.finalRoot]) {
    expect(Math.abs(row.horizontalEnd - row.circleLeft)).toBeLessThanOrEqual(0.5);
    expect(Math.abs(row.horizontalY - row.circleCenterY)).toBeLessThanOrEqual(0.5);
  }
  expect(Math.abs(geometry.bridgeX - geometry.parentCircleCenterX)).toBeLessThanOrEqual(0.5);
  expect(Math.abs(geometry.bridgeY - geometry.parentCircleBottom)).toBeLessThanOrEqual(0.5);
  expect(Math.abs(geometry.first.trunkX - geometry.parentCircleCenterX)).toBeLessThanOrEqual(0.5);
  expect(geometry.fontSize).toBe('12px');
  expect(geometry.parent.continues).toBe(true);
  expect(geometry.first.continues).toBe(true);
  expect(geometry.last.continues).toBe(false);
  expect(geometry.finalRoot.continues).toBe(false);
});
