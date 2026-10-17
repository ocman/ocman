// Display-only extraction. Never evaluate code supplied by an agent.
export function mcpToolIntent(toolName: string, args: string): string | null {
  let input: Record<string, unknown> | null;
  try { input = JSON.parse(args); } catch { return null; }
  if (!input || typeof input !== 'object' || Array.isArray(input)) return null;
  if (/^(?:mcp[_.:]+)?ocman[_.:]+/.test(toolName)) return intentText(input.intent);
  if (toolName !== 'execute' && toolName !== 'mcp_execute') return null;
  if (typeof input.code !== 'string') return null;

  const tokens = input.code.match(/\/\/[^\n]*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'|`(?:\\.|[^`\\])*`|[\w$]+|[^\s]/g)
    ?.filter((token) => !token.startsWith('//') && !token.startsWith('/*')) || [];
  const intents = new Set<string>();
  for (let i = 0; i < tokens.length; i++) {
    if (tokens[i] !== 'tools' || tokens[i - 1] === '.') continue;
    let cursor = i + 1;
    const member = () => {
      if (tokens[cursor] === '.') return tokens[++cursor] && tokens[cursor++];
      if (tokens[cursor] !== '[') return null;
      const value = literal(tokens[cursor + 1]);
      if (tokens[cursor + 2] !== ']') return null;
      cursor += 3;
      return value;
    };
    if (member() !== 'ocman' || !member() || tokens[cursor++] !== '(' || tokens[cursor++] !== '{') continue;
    for (const intent of objectIntents(tokens, cursor)) intents.add(intent);
  }
  return intents.size ? [...intents].join('; ') : null;
}

function objectIntents(tokens: string[], cursor: number): string[] {
  const intents: string[] = [];
  let depth = 1;
  for (; cursor < tokens.length && depth > 0; cursor++) {
    const token = tokens[cursor];
    if (depth === 1 && ['{', ','].includes(tokens[cursor - 1])
      && (token === 'intent' || literal(token) === 'intent') && tokens[cursor + 1] === ':'
      && [',', '}'].includes(tokens[cursor + 3])) {
      const intent = intentText(literal(tokens[cursor + 2]));
      if (intent) intents.push(intent);
    }
    if (['{', '[', '('].includes(token)) depth++;
    if (['}', ']', ')'].includes(token)) depth--;
  }
  return intents;
}

function intentText(value: unknown): string | null {
  return typeof value === 'string' ? value.trim().replace(/\s+/g, ' ') || null : null;
}

function literal(token: string | undefined): string | null {
  if (!token || !['"', "'"].includes(token[0])) return null;
  try {
    // JSON handles escapes without executing expressions. Normalize single-quoted JS strings.
    const json = token[0] === "'"
      ? `"${token.slice(1, -1).replace(/\\'|"/g, (value) => value === "\\'" ? "'" : '\\"')}"`
      : token;
    return JSON.parse(json);
  } catch { return null; }
}
