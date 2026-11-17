// @vitest-environment jsdom
import { render, screen, within } from '@testing-library/react';
import { describe, expect, it } from 'vitest';
import { KpiStrip } from './KpiStrip';

describe('KpiStrip', () => {
  it('renders labelled figures with details and a named help button', () => {
    render(<KpiStrip label="Summary" items={[
      { label: 'Error rate', value: '0.2%', detail: '1 of 449 failed' },
      { label: 'Cost', value: '$35.48', detail: 'API-equivalent estimate · $0 billed', help: 'How cost is computed' },
    ]} />);
    const strip = screen.getByLabelText('Summary');
    expect(within(strip).getByText('0.2%')).toBeInTheDocument();
    expect(within(strip).getByText('1 of 449 failed')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'About Cost' })).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: 'About Error rate' })).not.toBeInTheDocument();
  });
});
