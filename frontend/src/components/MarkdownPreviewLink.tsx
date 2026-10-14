import { useEffect, useEffectEvent, useId, useRef, useState, type ComponentProps, type CSSProperties, type RefObject } from 'react';
import { hasRichPreview, useProviderPreviews } from '../lib/useProviderPreviews';
import { Popover } from './Popover';
import { ProviderPreview } from './ProviderLinkPreview';
import './GitHubLinkPreview.css';

export function MarkdownPreviewLink({ href, children, ...props }: ComponentProps<'a'> & { href: string }) {
  const trigger = useRef<HTMLAnchorElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const hovered = useRef(false);
  const id = useId();
  const [activeURL, setActiveURL] = useState<string | null>(null);
  const [position, setPosition] = useState<CSSProperties>({});
  const active = activeURL === href;
  const cancel = () => clearTimeout(timer.current);
  const close = () => { cancel(); setActiveURL(null); };
  const closeOnScroll = useEffectEvent(close);
  const focused = () => trigger.current === document.activeElement || !!document.getElementById(id)?.contains(document.activeElement);

  useEffect(() => {
    const scroll = (event: Event) => {
      if (!(event.target instanceof Node) || !document.getElementById(id)?.contains(event.target)) closeOnScroll();
    };
    window.addEventListener('scroll', scroll, true);
    return () => { clearTimeout(timer.current); window.removeEventListener('scroll', scroll, true); };
  }, [href, id]);

  const enter = () => {
    cancel();
    if (active) return;
    timer.current = setTimeout(() => {
      const rect = trigger.current?.getBoundingClientRect();
      if (!rect) return;
      const below = window.innerHeight - rect.bottom;
      setPosition({
        left: Math.max(12, Math.min(rect.left, window.innerWidth - Math.min(340, window.innerWidth - 24) - 12)),
        ...(below >= 240 || below >= rect.top
          ? { top: rect.bottom + 6, maxHeight: Math.max(0, Math.min(560, below - 32)) }
          : { bottom: window.innerHeight - rect.top + 6, maxHeight: Math.max(0, Math.min(560, rect.top - 32)) }),
      });
      setActiveURL(href);
    }, 500);
  };
  const leave = () => {
    hovered.current = false;
    cancel();
    if (!focused()) timer.current = setTimeout(close, 150);
  };
  const blur = () => {
    cancel();
    timer.current = setTimeout(() => { if (!hovered.current && !focused()) close(); }, 150);
  };

  return <>
    <a {...props} ref={trigger} href={href} onMouseEnter={() => { hovered.current = true; enter(); }} onMouseLeave={leave}
      onFocus={enter} onBlur={blur} onClick={close}>
      {children}
    </a>
    {active && <LinkCard href={href} id={id} triggerRef={trigger} position={position} onClose={close}
      onEnter={() => { hovered.current = true; cancel(); }} onLeave={leave} onFocus={cancel} onBlur={blur} />}
  </>;
}

function LinkCard({ href, id, triggerRef, position, onClose, onEnter, onLeave, onFocus, onBlur }: {
  href: string;
  id: string;
  triggerRef: RefObject<HTMLAnchorElement | null>;
  position: CSSProperties;
  onClose: () => void;
  onEnter: () => void;
  onLeave: () => void;
  onFocus: () => void;
  onBlur: () => void;
}) {
  const { previews, providers, refreshChecks } = useProviderPreviews(href, true);
  const preview = previews.find(hasRichPreview);
  return <Popover open={!!preview} onClose={onClose} triggerRef={triggerRef} id={id}
    label="Link preview" className="oc-link-preview" autoFocus={false} style={position}>
    <div onMouseEnter={onEnter} onMouseLeave={onLeave} onFocus={onFocus} onBlur={onBlur}>
      {preview && <ProviderPreview preview={preview} providers={providers} refreshChecks={refreshChecks} />}
    </div>
  </Popover>;
}
