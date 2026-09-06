import type { MetricsPerformance } from '../../lib/api';
import { CHART_COLORS } from '../../lib/chartConfig';
import { renderModel } from '../../lib/format';

export function buildCostByModelDatasets(metrics: MetricsPerformance) {
  const { models, series } = metrics.dailyEstimatedCostByModel;
  if (models.length === 0) {
    return [{ label: 'Cost', data: series.map(() => 0), borderColor: '#a6e3a1', backgroundColor: 'rgba(166, 227, 161, 0.18)', stack: 'cost' }];
  }
  return models.map((model, index) => {
    const colour = CHART_COLORS[index % CHART_COLORS.length];
    const [r, g, b] = colour.match(/[a-f\d]{2}/gi)?.map((value) => parseInt(value, 16)) ?? [];
    return {
      label: renderModel(model),
      data: series.map((point) => point.costs?.[index] ?? 0),
      borderColor: colour,
      backgroundColor: r == null ? colour : `rgba(${r}, ${g}, ${b}, 0.72)`,
      stack: 'cost',
      borderWidth: 1,
    };
  });
}
