// Generate a simple unified diff between two strings.
// Shows context lines around changes for readability.
export function simpleDiff(oldStr: string, newStr: string, startLine = 1): string {
  const oldLines = oldStr.split('\n');
  const newLines = newStr.split('\n');

  // Find longest common subsequence to produce a proper diff
  const m = oldLines.length;
  const n = newLines.length;

  // For small inputs, use full LCS. For large inputs, just show removed/added.
  if (m + n > 200) {
    const out: string[] = [];
    oldLines.forEach(l => out.push(`- ${l}`));
    newLines.forEach(l => out.push(`+ ${l}`));
    return out.join('\n');
  }

  // Build LCS table
  const dp: number[][] = Array.from({ length: m + 1 }, () => new Array(n + 1).fill(0));
  for (let i = 1; i <= m; i++) {
    for (let j = 1; j <= n; j++) {
      dp[i][j] = oldLines[i - 1] === newLines[j - 1]
        ? dp[i - 1][j - 1] + 1
        : Math.max(dp[i - 1][j], dp[i][j - 1]);
    }
  }

  // Backtrack to produce diff
  const ops: Array<[string, string]> = [];
  let i = m, j = n;
  while (i > 0 || j > 0) {
    if (i > 0 && j > 0 && oldLines[i - 1] === newLines[j - 1]) {
      ops.push([' ', oldLines[i - 1]]);
      i--; j--;
    } else if (j > 0 && (i === 0 || dp[i][j - 1] >= dp[i - 1][j])) {
      ops.push(['+', newLines[j - 1]]);
      j--;
    } else {
      ops.push(['-', oldLines[i - 1]]);
      i--;
    }
  }
  ops.reverse();

  // Assign line numbers to each op
  let oldLine = startLine, newLine = startLine;
  const numbered: Array<[string, string, string, string]> = []; // [op, text, oldLn, newLn]
  ops.forEach(([op, text]) => {
    if (op === ' ') {
      numbered.push([op, text, String(oldLine), String(newLine)]);
      oldLine++; newLine++;
    } else if (op === '-') {
      numbered.push([op, text, String(oldLine), '']);
      oldLine++;
    } else {
      numbered.push([op, text, '', String(newLine)]);
      newLine++;
    }
  });

  // Only show lines around changes (context of 2 lines)
  const ctx = 2;
  const changed = new Set<number>();
  numbered.forEach(([op], idx) => {
    if (op !== ' ') {
      for (let k = Math.max(0, idx - ctx); k <= Math.min(numbered.length - 1, idx + ctx); k++) {
        changed.add(k);
      }
    }
  });

  const pad = (s: string, w: number) => s.padStart(w, ' ');
  const maxOld = String(oldLine).length;
  const maxNew = String(newLine).length;

  const result: string[] = [];
  let lastShown = -1;
  numbered.forEach(([op, text, oln, nln], idx) => {
    if (!changed.has(idx)) return;
    if (lastShown >= 0 && idx - lastShown > 1) {
      result.push(`${' '.repeat(maxOld)}  ${' '.repeat(maxNew)}    ...`);
    }
    const ol = oln ? pad(oln, maxOld) : ' '.repeat(maxOld);
    const nl = nln ? pad(nln, maxNew) : ' '.repeat(maxNew);
    result.push(`${ol}  ${nl}  ${op} ${text}`);
    lastShown = idx;
  });

  return result.join('\n');
}
