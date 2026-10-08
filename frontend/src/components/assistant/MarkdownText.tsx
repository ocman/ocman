// Markdown rendering for assistant text parts and tool output:
// react-markdown wired with stable plugin/component references plus a
// copy-button code block. Extracted from AssistantThread.tsx.
import { Children, cloneElement, Fragment, isValidElement, memo, useEffect, useId, useState } from 'react';
import { createPortal } from 'react-dom';
import ReactMarkdown from 'react-markdown';
import type { ExtraProps } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import remarkBreaks from 'remark-breaks';
import rehypeHighlight from 'rehype-highlight';
import { TransformComponent, TransformWrapper } from 'react-zoom-pan-pinch';
import type { ComponentProps, FC, ReactNode } from 'react';
import { Link, useInRouterContext } from 'react-router-dom';
import { LinkPreviewStrip } from '../GitHubLinkPreview';
import { FactoryActionCard } from '../FactoryActionCard';
import { FactoryMarkerCard } from '../FactoryMarkerCard';
import { factoryActionFromHref } from '../factoryEpicStatus';
import { remarkFactoryCards } from '../factoryCards';
import { Modal } from '../Modal';
import { CopyButton } from '../CopyButton';
import { splitMarkdownBlocks } from './markdownBlocks';
import './Reasoning.css';

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

function nodeText(node: ReactNode): string {
  if (typeof node === 'string' || typeof node === 'number') return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join('');
  if (isValidElement<{ children?: ReactNode }>(node)) return nodeText(node.props.children);
  return '';
}

export function ZoomableGraphicModal({ label, closeLabel, maxScale = 4, onClose, children }: {
  label: string;
  closeLabel: string;
  maxScale?: number;
  onClose: () => void;
  children: ReactNode;
}) {
  const [zoom, setZoom] = useState(1);
  return createPortal(
    <Modal label={label} onClose={onClose} backdropClassName="oc-mermaid-modal-backdrop" dialogClassName="oc-mermaid-modal">
      <TransformWrapper
        minScale={0.25}
        maxScale={maxScale}
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
              <button type="button" aria-label={closeLabel} title={closeLabel} onClick={onClose}>
                <i className="bi bi-x-lg" aria-hidden="true" />
              </button>
            </div>
            <TransformComponent
              wrapperClass="oc-mermaid-modal-viewport"
              contentClass="oc-mermaid-modal-content"
              wrapperStyle={{ width: '100%', height: '100%' }}
              contentStyle={{ width: '100%', height: '100%' }}
              wrapperProps={{ 'aria-label': `${label} viewport` }}
            >
              {children}
            </TransformComponent>
          </>
        )}
      </TransformWrapper>
    </Modal>,
    document.body,
  );
}

function MermaidDiagram({ source }: { source: string }) {
  const id = `oc-mermaid-${useId().replaceAll(':', '')}`;
  const [result, setResult] = useState({ source: '', svg: '', failed: false });
  const [expanded, setExpanded] = useState(false);

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
        onClick={() => setExpanded(true)}
        dangerouslySetInnerHTML={{ __html: result.svg }}
      />
      {expanded && (
        <ZoomableGraphicModal label="Mermaid diagram" closeLabel="Close diagram" onClose={() => setExpanded(false)}>
          <div className="oc-mermaid-modal-diagram" dangerouslySetInnerHTML={{ __html: result.svg }} />
        </ZoomableGraphicModal>
      )}
    </>
  );
}

// Older transcripts contain absolute ocman file URLs from before proxy-safe embeds.
function relativeFileURL(href: string | undefined): string | undefined {
  if (!href || href.startsWith('#') || !URL.canParse(href, window.location.href)) return href;
  const url = new URL(href, window.location.href);
  if (!['http:', 'https:'].includes(url.protocol)) return href;
  if (url.origin === window.location.origin || /^\/api\/(?:file\/[^/]+$|artifacts\/[^/]+\/files\/\d+$)/.test(url.pathname)) {
    return url.pathname + url.search + url.hash;
  }
  return href;
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
function MarkdownImage({ node: _node, alt = '', src, ...props }: ComponentProps<'img'> & { node?: unknown }) {
  const [expanded, setExpanded] = useState(false);
  const label = alt || 'Image';
  const image = relativeFileURL(src);

  return (
    <>
      <button type="button" className="oc-md-image" aria-label={`Expand ${label}`} onClick={() => setExpanded(true)}>
        <img alt={alt} src={image} {...props} />
      </button>
      {expanded && (
        <ZoomableGraphicModal label={label} closeLabel="Close image" maxScale={8} onClose={() => setExpanded(false)}>
          <img className="oc-image-modal-graphic" alt={alt} src={image} {...props} />
        </ZoomableGraphicModal>
      )}
    </>
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function CodeBlockPre(props: any) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { children, node: _node, ...rest } = props;
  const code = Array.isArray(children) ? children[0] : children;
  if (isValidElement<{ className?: string; children?: ReactNode }>(code) && code.props.className?.split(' ').includes('language-mermaid')) {
    return <MermaidDiagram source={nodeText(code.props.children)} />;
  }
  return (
    <div className="oc-code-block">
      <CopyButton className="oc-code-copy" iconOnly size="compact" label="Copy code" text={nodeText(children)} />
      <pre {...rest}>{children}</pre>
    </div>
  );
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any
function MarkdownLink(props: any) {
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  const { node: _node, href: originalHref, children, ...rest } = props;
  const href = relativeFileURL(originalHref);
  const routed = useInRouterContext();
  if (props['data-ocman-card'] && routed) {
    return <FactoryMarkerCard epicID={props['data-ocman-epic']} issueID={props['data-ocman-issue']} action={props['data-ocman-action']}>{children}</FactoryMarkerCard>;
  }
  const action = factoryActionFromHref(href);
  if (action && routed) return <FactoryActionCard key={`${action.epicID}/${action.issueID}`} {...action}>{children}</FactoryActionCard>;
  const internal = href?.startsWith('/') && !href.startsWith('//') && !/^\/api(?:\/|$)/.test(href);
  // In-app paths must not reload the page; anchors and externals stay plain.
  if (internal && routed) return <Link {...rest} to={href}>{children}</Link>;
  const local = internal || href?.startsWith('#');
  return <a {...rest} href={href} target={local ? undefined : '_blank'} rel={local ? undefined : 'noopener noreferrer'}>{children}</a>;
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
function MarkdownTable({ node: _node, ...props }: ComponentProps<'table'> & { node?: unknown }) {
  return (
    <div role="region" aria-label="Scrollable table" tabIndex={0} style={{ maxWidth: '100%', overflowX: 'auto' }}>
      <table {...props} />
    </div>
  );
}

// Module-scoped to keep prop references stable across renders. Fresh
// array/object literals here would invalidate react-markdown's
// internal unified-processor cache on every streaming chunk.
const REMARK_PLUGINS = [remarkGfm, remarkFactoryCards];
const REMARK_PLUGINS_WITH_BREAKS = [...REMARK_PLUGINS, remarkBreaks];
// rehype-highlight builds a lowlight instance and registers ~37 languages
// each time it is attached, and react-markdown attaches plugins on every
// render; reuse one transformer.
let highlightTransformer: ReturnType<typeof rehypeHighlight> | undefined;
const sharedRehypeHighlight = () => (highlightTransformer ??= rehypeHighlight());
const REHYPE_PLUGINS = [sharedRehypeHighlight];

function removeTrailingDuration(node: ReactNode, duration: string): ReactNode {
  let remaining = duration.length;
  const text = nodeText(node);
  let trailing = text.length - text.trimEnd().length;
  function remove(child: ReactNode): ReactNode {
    if (typeof child === 'string') {
      const end = Math.max(0, child.length - trailing);
      trailing = Math.max(0, trailing - child.length);
      const count = Math.min(remaining, end);
      remaining -= count;
      return child.slice(0, end - count) + child.slice(end);
    }
    if (isValidElement<{ children?: ReactNode }>(child)) {
      return cloneElement(child, { children: remove(child.props.children) });
    }
    const children: ReactNode[] = Children.toArray(child);
    for (let i = children.length - 1; i >= 0 && remaining > 0; i--) children[i] = remove(children[i]);
    return children;
  }
  return remove(node);
}

// eslint-disable-next-line @typescript-eslint/no-unused-vars
function MarkdownQuote({ children, node: _node, ...props }: ComponentProps<'blockquote'> & ExtraProps) {
  const blocks: ReactNode[] = Children.toArray(children);
  const firstIndex = blocks.findIndex(child => isValidElement(child));
  const first = blocks[firstIndex];
  if (!isValidElement<{ children?: ReactNode }>(first) || (first.type !== 'p' && first.type !== MarkdownParagraph)) return <blockquote {...props}>{children}</blockquote>;
  const label = Children.toArray(first.props.children)[0];
  if (!isValidElement(label) || label.type !== 'strong' || !/^(Thinking|Thought):$/.test(nodeText(label))) {
    return <blockquote {...props}>{children}</blockquote>;
  }
  const lastIndex = blocks.findLastIndex(child => isValidElement(child));
  const duration = lastIndex > firstIndex
    ? nodeText(blocks[lastIndex]).trimEnd().match(/ · \d+(?:\.\d+)?[smhd](?: \d+[smhd])?$/)?.[0]
    : undefined;
  if (duration) blocks[lastIndex] = removeTrailingDuration(blocks[lastIndex], duration);
  return (
    <blockquote className="oc-reasoning">
      <details>
        <summary>{first.props.children}{duration}</summary>
        {blocks.slice(firstIndex + 1)}
      </details>
    </blockquote>
  );
}

function containsFactoryCard(children: ReactNode): boolean {
  return Children.toArray(children).some((child) => {
    if (!isValidElement<{ href?: string; children?: ReactNode; 'data-ocman-card'?: string }>(child)) return false;
    return Boolean(child.props['data-ocman-card'] || factoryActionFromHref(child.props.href) || containsFactoryCard(child.props.children));
  });
}

// Factory approval cards include block Markdown; a paragraph cannot contain them.
// eslint-disable-next-line @typescript-eslint/no-unused-vars
function MarkdownParagraph({ children, node: _node, ...props }: ComponentProps<'p'> & ExtraProps) {
  return containsFactoryCard(children) ? <div {...props}>{children}</div> : <p {...props}>{children}</p>;
}

const MARKDOWN_COMPONENTS = { p: MarkdownParagraph, pre: CodeBlockPre, a: MarkdownLink, img: MarkdownImage, table: MarkdownTable, blockquote: MarkdownQuote };

// One independently parsed chunk. memo: while an answer streams only the
// last chunk's text changes, so earlier chunks skip re-parsing.
const MarkdownBlock = memo(function MarkdownBlock({ text, preserveLineBreaks }: { text: string; preserveLineBreaks: boolean }) {
  return (
    <ReactMarkdown
      remarkPlugins={preserveLineBreaks ? REMARK_PLUGINS_WITH_BREAKS : REMARK_PLUGINS}
      rehypePlugins={REHYPE_PLUGINS}
      components={MARKDOWN_COMPONENTS}
    >
      {text}
    </ReactMarkdown>
  );
});

export const MarkdownContent: FC<{ text: string; preserveLineBreaks?: boolean }> = ({ text, preserveLineBreaks = false }) => {
  if (!text.trim()) return null;
  // The '\n' between chunks is the whitespace node a single parse emits
  // between top-level blocks, so the DOM is identical.
  return splitMarkdownBlocks(text).map((block, i) => (
    <Fragment key={i}>
      {i > 0 && '\n'}
      <MarkdownBlock text={block} preserveLineBreaks={preserveLineBreaks} />
    </Fragment>
  ));
};

export const MarkdownText: FC<{ text: string }> = ({ text }) => (
  <>
    <MarkdownContent text={text} />
    {text.trim() && <LinkPreviewStrip text={text} />}
  </>
);
