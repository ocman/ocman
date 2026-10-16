/** Display-only recognition of literal Code Mode calls, never execution or routing. */
export function mcpToolLabel(toolName: string, args: string): string | null {
  if (toolName !== 'execute' && toolName !== 'mcp_execute') return null;
  let code: unknown;
  try { code = (JSON.parse(args) as { code?: unknown } | null)?.code; } catch { return null; }
  if (typeof code !== 'string') return null;

  // Skip comments and quoted examples. Only recognize literal server/tool paths.
  const tokens = /\/\/[^\n]*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|\btools\s*(?:\.\s*([\w$-]+)|\[\s*['"]([\w$-]+)['"]\s*\])\s*(?:\.\s*([\w$-]+)|\[\s*['"]([\w$-]+)['"]\s*\])\s*\(|\b(search)\s*\(/g;
  const calls = new Set<string>();
  let discovery = false;
  for (const match of code.matchAll(tokens)) {
    if (match[5]) discovery = true;
    else if (match[1] || match[2]) calls.add(`${match[1] || match[2]}.${match[3] || match[4]}`);
  }
  if (calls.size) return `MCP ${[...calls].join(', ')}`;
  return discovery ? 'MCP tool discovery' : null;
}
