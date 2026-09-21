// @vitest-environment jsdom
import { Chart } from 'chart.js';
import { expect, it } from 'vitest';
import './Dashboard';
import { LINE_OPTIONS_DATABASE_SIZE } from '../lib/chartConfig';

it('registers the logarithmic scale used by the database size chart', () => {
  expect(Chart.registry.getScale(LINE_OPTIONS_DATABASE_SIZE.scales.y.type)).toHaveProperty('id', 'logarithmic');
});
