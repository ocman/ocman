// @vitest-environment jsdom
import { afterEach, describe, expect, it } from 'vitest';
import { render, screen } from '@testing-library/react';
import { PerfProfiler } from './PerfProfiler';
import { _resetPerfMonitorForTests, summary } from '../lib/perfMonitor';

afterEach(() => _resetPerfMonitorForTests());

describe('PerfProfiler', () => {
  it('renders children without profiling when perf monitoring is off', () => {
    _resetPerfMonitorForTests(false);
    render(<PerfProfiler id="Thread"><p>child</p></PerfProfiler>);
    expect(screen.getByText('child')).toBeInTheDocument();
    expect(summary().counters).toEqual({});
  });

  it('records commits for the subtree when on', () => {
    _resetPerfMonitorForTests(true);
    render(<PerfProfiler id="Thread"><p>child</p></PerfProfiler>);
    expect(screen.getByText('child')).toBeInTheDocument();
    expect(summary().counters['commits|Thread|mount']).toBe(1);
  });
});
