// Markdown rendering for assistant text parts and tool output:
// react-markdown wired with stable plugin/component references plus a
// copy-button code block. Extracted from AssistantThread.tsx.
import { isValidElement, useEffect, useId, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { TransformComponent, TransformWrapper } from 'react-zoom-pan-pinch';
import type { FC, ReactNode } from 'react';
import { LinkPreviewStrip } from '../GitHubLinkPreview';
import { Modal } from '../Modal';

let mermaidPromise: Promise<typeof import('mermaid')['default']> | undefined;
function loadMermaid() {
  return mermaidPromise ??= import('mermaid').then(({ default: mermaid }) => {
    mermaid.initialize({
      startOnLoad: false,
      securityLevel: 'strict',
      theme: 'base',
      themeVariables: {
        darkMode: true,
        background: '#181825',
        primaryColor: '#313244',
        primaryBorderColor: '#89b4fa',
        primaryTextColor: '#cdd6f4',
        secondaryColor: '#45475a',
        tertiaryColor: '#313244',
        textColor: '#cdd6f4',
        lineColor: '#a6adc8',
        actorBkg: '#313244',
        actorBorder: '#89b4fa',
        actorTextColor: '#cdd6f4',
        actorLineColor: '#7f849c',
        signalColor: '#a6adc8',
        signalTextColor: '#cdd6f4',
        labelBoxBkgColor: '#313244',
        labelBoxBorderColor: '#585b70',
        labelTextColor: '#cdd6f4',
        loopTextColor: '#cdd6f4',
        noteBkgColor: '#313244',
        noteBorderColor: '#fab387',
        noteTextColor: '#cdd6f4',
        activationBkgColor: '#45475a',
        activationBorderColor: '#89b4fa',
      },
    });
    return mermaid;
  });
}

function fallbackCopy(text: string) {
  const el = document.createElement('div');
  el.contentEditable = 'true';
  el.style.position = 'fixed';
  el.style.opacity = '0';
  el.innerText = text;
  document.body.appendChild(el);
  // iOS requires selecting a range inside a contenteditable element
  const range = document.createRange();
  range.selectNodeContents(el);
  const sel = window.getSelection();
  sel?.removeAllRanges();
  sel?.addRange(range);
  document.execCommand('copy');
  document.body.removeChild(el);
}

function nodeText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join('');
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children);
  return '';
}

function MermaidDiagram({ source }: { source: string }) {
  const id = `oc-mermaid-${useId().replaceAll(':', '')}`;
  const [result, setResult] = useState({ source: '', svg: '', failed: false });
  const [expanded, setExpanded] = useState(false);
  const [zoom, setZoom] = useState(1);

  useEffect(() => {
    let active = true;
    loadMermaid().then((mermaid) => mermaid.render(id, source)).then(
      ({ svg }) => { if (active) setResult({ source, svg, failed: false }); },
      () => { if (active) setResult({ source, svg: '', failed: true }); },
    );
    return () => { active = false; };
  }, [id, source]);

  if (result.source === source && result.failed) return <pre><code>{source}</code></pre>;
  if (result.source !== source) return <div className="oc-mermaid" aria-label="Mermaid diagram" />;
  return (
    <>
      <button
        type="button"
        className="oc-mermaid"
        aria-label="Expand Mermaid diagram"
        onClick={() => { setZoom(1); setExpanded(true); }}
        dangerouslySetInnerHTML={{ __html: result.svg }}
      />
      {expanded && createPortal(
        <Modal label="Mermaid diagram" onClose={() => setExpanded(false)} backdropClassName="oc-mermaid-modal-backdrop" dialogClassName="oc-mermaid-modal">
          <TransformWrapper
            minScale={0.25}
            maxScale={4}
            centerOnInit
            centerZoomedOut
            disablePadding
            smooth={false}
            wheel={{ step: 0.01 }}
            pinch={{ step: 8, disabled: false, allowPanning: true }}
            panning={{ allowMiddleClickPan: false, allowRightClickPan: false }}
            doubleClick={{ disabled: true }}
            onTransform={(_, state) => setZoom(state.scale)}
          >
            {({ zoomIn, zoomOut }) => (
              <>
                <div className="oc-mermaid-modal-toolbar">
                  <button type="button" aria-label="Zoom out" title="Zoom out" onClick={() => zoomOut(0.25, 0)}>
                    <i className="bi bi-dash-lg" aria-hidden="true" />
                  </button>
                  <span>{Math.round(zoom * 100)}%</span>
                  <button type="button" aria-label="Zoom in" title="Zoom in" onClick={() => zoomIn(0.25, 0)}>
                    <i className="bi bi-plus-lg" aria-hidden="true" />
                  </button>
                  <button type="button" aria-label="Close diagram" title="Close diagram" onClick={() => setExpanded(false)}>
                    <i className="bi bi-x-lg" aria-hidden="true" />
                  </button>
                </div>
                <TransformComponent
                  wrapperClass="oc-mermaid-modal-viewport"
                  contentClass="oc-mermaid-modal-content"
                  wrapperStyle={{ width: '100%', height: '100%' }}
                  contentStyle={{ width: '100%', height: '100%' }}
                  wrapperProps={{ 'aria-label': 'Mermaid diagram viewport' }}
                >
                  <div className="oc-mermaid-modal-diagram" dangerouslySetInnerHTML={{ __html: result.svg }} />
                </TransformComponent>
              </>
            )}
          </TransformWrapper>
        </Modal>,
        document.body,
      )}
    </>
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function CodeBlockPre(props: any) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { children, node: _node, ...rest } = props;
  const codeRef = useRef<HTMLPreElement>(null);
  const [copied, setCopied] = useState(false);
  const handleCopy = () => {
    const text = codeRef.current?.textContent || '';
    // Show feedback immediately — don't wait for the async clipboard promise
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
    if (navigator.clipboard) {
      navigator.clipboard.writeText(text).catch(() => fallbackCopy(text));
    } else {
      fallbackCopy(text);
    }
  };
  const code = Array.isArray(children) ? children[0] : children;
  if (isValidElement<{ className?: string; children?: ReactNode }>(code) && code.props.className?.split(' ').includes('language-mermaid')) {
    return <MermaidDiagram source={nodeText(code.props.children)} />;
  }
  return (
    <div className="oc-code-block">
      <button className={`oc-code-copy${copied ? ' oc-code-copy--copied' : ''}`} onClick={handleCopy} title="Copy code">
        <i className={`bi ${copied ? 'bi-check2' : 'bi-copy'}`} aria-hidden="true" />
      </button>
      <pre ref={codeRef} {...rest}>{children}</pre>
    </div>
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function MarkdownLink(props: any) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { node: _node, ...rest } = props;
  return <a {...rest} target="_blank" rel="noopener noreferrer" />;
}

// Module-scoped to keep prop references stable across renders. Fresh
// array/object literals here would invalidate react-markdown's
// internal unified-processor cache on every streaming chunk.
const REMARK_PLUGINS = [remarkGfm];
const REHYPE_PLUGINS = [rehypeHighlight];
const MARKDOWN_COMPONENTS = { pre: CodeBlockPre, a: MarkdownLink };

export const MarkdownContent: FC<{ text: string }> = ({ text }) => {
  if (!text.trim()) return null;
  return (
    <ReactMarkdown
      remarkPlugins={REMARK_PLUGINS}
      rehypePlugins={REHYPE_PLUGINS}
      components={MARKDOWN_COMPONENTS}
    >
      {text}
    </ReactMarkdown>
  );
};

export const MarkdownText: FC<{ text: string }> = ({ text }) => (
  <>
    <MarkdownContent text={text} />
    {text.trim() && <LinkPreviewStrip text={text} />}
  </>
);
