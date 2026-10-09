// Factory IDs are epic slugs followed by child indices. Never expand URLs,
// existing links, fenced code, or code snippets containing more than an ID.
type MarkdownNode = { type: string; value?: string; children?: MarkdownNode[]; url?: string };
const ISSUE = /(?<![\w/.-])#?([a-z0-9]+(?:-[a-z0-9]+)*\.\d+(?:\.\d+)*)(?![\w/-]|\.\w)/g;

export function remarkFactoryIssueReferences() {
  function transform(node: MarkdownNode) {
    if (!node.children || ['link', 'linkReference', 'code'].includes(node.type)) return;
    node.children = node.children.flatMap((child): MarkdownNode[] => {
      if (!['text', 'inlineCode'].includes(child.type)) { transform(child); return [child]; }
      const text = child.value ?? '';
      const matches = [...text.matchAll(ISSUE)];
      if (child.type === 'inlineCode' && (matches.length !== 1 || matches[0][0] !== text)) return [child];
      const parts: MarkdownNode[] = [];
      let offset = 0;
      for (const match of matches) {
        // ponytail: leave versions and ordinary numbers literal; explicit issue links still work.
        if (/^v?\d+(?:\.\d+)+$/.test(match[1])) continue;
        parts.push({ type: 'text', value: text.slice(offset, match.index) });
        parts.push({ type: 'link', url: `/factory/issues/${encodeURIComponent(match[1])}`, children: [{ type: child.type, value: match[0] }] });
        offset = match.index + match[0].length;
      }
      if (!offset) return [child];
      parts.push({ type: 'text', value: text.slice(offset) });
      return parts;
    });
  }
  return transform;
}
