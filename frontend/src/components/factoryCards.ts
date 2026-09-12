// Keep card requests separate from ordinary markdown links. Only text nodes are
// expanded, so examples inside code blocks, inline code, and links stay literal.
type MarkdownNode = { type: string; value?: string; children?: MarkdownNode[] };
const CARD = /\[\[ocman:card type=(factory-epic|factory-issue)(?: epic=([^\s\]]+))?(?: issue=([^\s\]]+))? action=([a-z_]+)\]\]/g;
const ACTIONS = new Set(['created', 'create', 'pour', 'claim_plan', 'reopen_issue', 'reopen', 'mutate_graph', 'submit_proposal', 'approve_plan', 'revise_plan', 'reject_plan', 'resume_recovery', 'retry_recovery', 'cancel_recovery', 'approve_authority', 'reject_authority', 'save_formula', 'set_capacity_policy']);

export function remarkFactoryCards() {
  function transform(node: MarkdownNode) {
    if (!node.children || node.type === 'link' || node.type === 'linkReference') return;
    node.children = node.children.flatMap((child): MarkdownNode[] => {
      if (child.type !== 'text') { transform(child); return [child]; }
      const text = child.value ?? '';
      const parts: MarkdownNode[] = [];
      let offset = 0;
      for (const match of text.matchAll(CARD)) {
        const [, type, epic = '', issue = '', action] = match;
        if (!ACTIONS.has(action) || (type === 'factory-issue' && (!epic || !issue)) || (action === 'created' && (!epic || type !== 'factory-epic'))) continue;
        let epicID: string, issueID: string;
        try { epicID = decodeURIComponent(epic); issueID = decodeURIComponent(issue); } catch { continue; }
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
