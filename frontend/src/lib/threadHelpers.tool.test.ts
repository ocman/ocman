import { describe, it, expect } from 'vitest';
import { parseToolTime, parseShellDescription, formatToolDuration } from './threadHelpers';

describe('parseShellDescription', () => {
  it('strips the @desc: line and returns its text', () => {
    const out = parseShellDescription('completed\n@desc:Amend it\ngit commit\nmore');
    expect(out.description).toBe('Amend it');
    expect(out.strippedArgs).toBe('completed\ngit commit\nmore');
  });

  it('leaves args untouched when no marker is present', () => {
    const out = parseShellDescription("completed\ncat <<'X'\n@desc: not a marker\nX");
    expect(out.description).toBe('');
    expect(out.strippedArgs).toBe("completed\ncat <<'X'\n@desc: not a marker\nX");
  });
});

describe('parseToolTime', () => {
  it('extracts timing from a @time: line after the status', () => {
    const result = parseToolTime('completed\n@time:1000,2000\nTitle\ndetail');
    expect(result).not.toBeNull();
    expect(result!.startedAt).toBe(1000);
    expect(result!.completedAt).toBe(2000);
    expect(result!.strippedArgs).toBe('completed\nTitle\ndetail');
  });

  it('returns null when no @time: line is present', () => {
    expect(parseToolTime('completed\nTitle\ndetail')).toBeNull();
  });

  it('returns null when startedAt is 0', () => {
    expect(parseToolTime('completed\n@time:0,2000')).toBeNull();
  });

  it('handles completedAt of 0 (running tool)', () => {
    const result = parseToolTime('running\n@time:5000,0\ncommand');
    expect(result).not.toBeNull();
    expect(result!.startedAt).toBe(5000);
    expect(result!.completedAt).toBe(0);
    expect(result!.strippedArgs).toBe('running\ncommand');
  });

  it('handles @time: as the only line after status', () => {
    const result = parseToolTime('completed\n@time:100,200');
    expect(result).not.toBeNull();
    expect(result!.startedAt).toBe(100);
    expect(result!.completedAt).toBe(200);
    expect(result!.strippedArgs).toBe('completed');
  });

  it('handles empty argsText', () => {
    expect(parseToolTime('')).toBeNull();
  });

  it('handles large unix-ms timestamps', () => {
    const result = parseToolTime('completed\n@time:1714000000000,1714000005000');
    expect(result).not.toBeNull();
    expect(result!.startedAt).toBe(1714000000000);
    expect(result!.completedAt).toBe(1714000005000);
  });
});

describe('formatToolDuration', () => {
  it('returns empty string for negative values', () => {
    expect(formatToolDuration(-1)).toBe('');
  });

  it('returns "< 1s" for sub-second durations', () => {
    expect(formatToolDuration(0)).toBe('< 1s');
    expect(formatToolDuration(500)).toBe('< 1s');
    expect(formatToolDuration(999)).toBe('< 1s');
  });

  it('formats seconds with one decimal', () => {
    expect(formatToolDuration(1000)).toBe('1.0s');
    expect(formatToolDuration(1500)).toBe('1.5s');
    expect(formatToolDuration(30000)).toBe('30.0s');
    expect(formatToolDuration(59999)).toBe('60.0s');
  });

  it('formats minutes and seconds', () => {
    expect(formatToolDuration(60000)).toBe('1m 0s');
    expect(formatToolDuration(90000)).toBe('1m 30s');
    expect(formatToolDuration(3599000)).toBe('59m 59s');
  });

  it('formats hours and minutes', () => {
    expect(formatToolDuration(3600000)).toBe('1h 0m');
    expect(formatToolDuration(5400000)).toBe('1h 30m');
  });
});
