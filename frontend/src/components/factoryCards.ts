// Keep card requests separate from ordinary markdown links. Only text nodes are
// expanded, so examples inside code blocks, inline code, and links stay literal.
type MarkdownNode = { type: string; value?: string; children?: MarkdownNode[] };
const CARD = /\[\[ocman:card type=(factory-epic|factory-issue)(?: epic=([^\s\]]+))?(?: issue=([^\s\]]+))? action=([a-z_]+)\]\]/g;
const ACTIONS = new Set(['created', 'create', 'pour', 'claim_plan', 'reopen_issue', 'reopen', 'mutate_graph', 'submit_proposal', 'request_project', 'approve_plan', 'revise_plan', 'reject_plan', 'resume_recovery', 'retry_recovery', 'cancel_recovery', 'approve_authority', 'reject_authority', 'save_formula', 'set_capacity_policy']);

export type FactoryCardMarker = { type: string; epicID: string; issueID: string; action: string };

function parseCard(match: RegExpMatchArray): FactoryCardMarker | null {
  const [, type, epic = '', issue = '', action] = match;
  if (!ACTIONS.has(action) || (type === 'factory-issue' && (!epic || !issue)) || (action === 'created' && (!epic || type !== 'factory-epic'))) return null;
  try { return { type, epicID: decodeURIComponent(epic), issueID: decodeURIComponent(issue), action }; } catch { return null; }
}

// The first valid card marker in a Factory tool result, so the tool call can
// render the card itself instead of relying on the agent to copy the marker.
export function factoryCardFromToolResult(toolName: string, output: string): FactoryCardMarker | null {
  if (!/(^|_)factory$/i.test(toolName)) return null;
  for (const match of output.matchAll(CARD)) {
    const card = parseCard(match);
    if (card) return card;
  }
  return null;
}

export function remarkFactoryCards() {
  function transform(node: MarkdownNode) {
    if (!node.children || node.type === 'link' || node.type === 'linkReference') return;
    node.children = node.children.flatMap((child): MarkdownNode[] => {
      if (child.type !== 'text') { transform(child); return [child]; }
      const text = child.value ?? '';
      const parts: MarkdownNode[] = [];
      let offset = 0;
      for (const match of text.matchAll(CARD)) {
        const parsed = parseCard(match);
        if (!parsed) continue;
        const { type, epicID, issueID, action } = parsed;
        parts.push({ type: 'text', value: text.slice(offset, match.index) });
        const card = {
          type: 'link', url: epicID ? `/factory/epics/${encodeURIComponent(epicID)}` : '/factory/overview',
          data: { hProperties: { 'data-ocman-card': type, 'data-ocman-action': action, 'data-ocman-epic': epicID, 'data-ocman-issue': issueID } },
          children: [{ type: 'text', value: action === 'created' ? 'Open created epic' : 'Factory actions' }],
        };
        parts.push(card);
        offset = match.index + match[0].length;
      }
      parts.push({ type: 'text', value: text.slice(offset) });
      return parts;
    });
  }
  return transform;
}
