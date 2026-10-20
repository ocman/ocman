import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import type { ComponentProps, ReactNode } from 'react';
import { useInRouterContext, useLocation, useNavigate } from 'react-router-dom';
import { IconButton } from './IconButton';
import { Button } from './Control';
import { ZoomableGraphicModal } from './ZoomableGraphicModal';
import './ImagePreview.css';

type ImageEntry = { src: string; alt: string; element: HTMLButtonElement };
type Gallery = {
  register: (entry: ImageEntry) => () => void;
  open: (element: HTMLButtonElement) => void;
};
const ImagePreviewContext = createContext<Gallery | null>(null);

// Keep URLs short even for base64 attachments, and stable across page reloads.
function imageKey(src: string) {
  let first = 2166136261;
  let second = 5381;
  for (let i = 0; i < src.length; i++) {
    first = Math.imul(first ^ src.charCodeAt(i), 16777619);
    second = Math.imul(second, 33) ^ src.charCodeAt(i);
  }
  return `${(first >>> 0).toString(36)}.${(second >>> 0).toString(36)}`;
}

function GalleryProvider({ children, selected, select }: {
  children: ReactNode;
  selected: string | null;
  select: (id: string | null, replace?: boolean) => void;
}) {
  const [entries, setEntries] = useState<(ImageEntry & { key: string })[]>([]);
  const register = useCallback((entry: ImageEntry) => {
    const registered = { ...entry, key: imageKey(entry.src) };
    setEntries(current => [...current, registered]);
    return () => setEntries(current => current.filter(item => item !== registered));
  }, []);
  const images = useMemo(() => {
    const counts = new Map<string, number>();
    return [...entries].sort((a, b) => a.element.compareDocumentPosition(b.element) & Node.DOCUMENT_POSITION_FOLLOWING ? -1 : 1)
      .map(entry => {
        const key = entry.key;
        const occurrence = counts.get(key) ?? 0;
        counts.set(key, occurrence + 1);
        return { ...entry, id: `${key}-${occurrence}` };
      });
  }, [entries]);
  const index = images.findIndex(image => image.id === selected);
  const active = images[index];
  const open = useCallback((element: HTMLButtonElement) => {
    const image = images.find(item => item.element === element);
    if (image) select(image.id);
  }, [images, select]);
  const gallery = useMemo(() => ({ register, open }), [register, open]);

  useEffect(() => {
    if (!active) return;
    function onKey(event: KeyboardEvent) {
      const focusedDialog = document.activeElement?.closest('[role="dialog"]');
      if (focusedDialog && !focusedDialog.querySelector('.oc-image-modal-graphic')) return;
      if (event.altKey || event.ctrlKey || event.metaKey || event.shiftKey) return;
      if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return;
      event.preventDefault();
      event.stopImmediatePropagation();
      const next = images[index + (event.key === 'ArrowLeft' ? -1 : 1)];
      if (next) select(next.id, true);
    }
    window.addEventListener('keydown', onKey, true);
    return () => window.removeEventListener('keydown', onKey, true);
  }, [active, images, index, select]);

  return <ImagePreviewContext.Provider value={gallery}>
    {children}
    {active && <ZoomableGraphicModal key={active.id} label={active.alt || 'Image'} closeLabel="Close image" maxScale={8}
      onClose={() => select(null, true)} navigation={images.length > 1 && <>
        <IconButton label="Previous image" icon="bi-chevron-left" variant="ghost" disabled={index === 0} onClick={() => select(images[index - 1].id, true)} />
        <span>{index + 1} / {images.length}</span>
        <IconButton label="Next image" icon="bi-chevron-right" variant="ghost" disabled={index === images.length - 1} onClick={() => select(images[index + 1].id, true)} />
      </>}>
      <img className="oc-image-modal-graphic" src={active.src} alt={active.alt} />
    </ZoomableGraphicModal>}
  </ImagePreviewContext.Provider>;
}

function RoutedProvider({ children }: { children: ReactNode }) {
  const location = useLocation();
  const navigate = useNavigate();
  const selected = new URLSearchParams(location.search).get('image');
  const select = useCallback((id: string | null, replace = false) => {
    const params = new URLSearchParams(location.search);
    if (id === null) params.delete('image');
    else params.set('image', id);
    void navigate({ pathname: location.pathname, search: params.toString(), hash: location.hash }, { replace, preventScrollReset: true });
  }, [location, navigate]);
  return <GalleryProvider selected={selected} select={select}>{children}</GalleryProvider>;
}

function LocalProvider({ children }: { children: ReactNode }) {
  const [selected, select] = useState<string | null>(null);
  return <GalleryProvider selected={selected} select={select}>{children}</GalleryProvider>;
}

export function ImagePreviewProvider({ children }: { children: ReactNode }) {
  return useInRouterContext() ? <RoutedProvider>{children}</RoutedProvider> : <LocalProvider>{children}</LocalProvider>;
}

type PreviewImageProps = ComponentProps<'img'> & { buttonClassName?: string };

function RegisteredImage({ src = '', alt = '', buttonClassName, ...props }: PreviewImageProps) {
  const { register, open } = useContext(ImagePreviewContext)!;
  const ref = useRef<HTMLButtonElement>(null);
  useEffect(() => {
    if (ref.current && src) return register({ src, alt, element: ref.current });
  }, [register, src, alt]);
  return <Button ref={ref} type="button" className={`oc-image-preview-trigger ${buttonClassName ?? ''}`} aria-label={`Expand ${alt || 'Image'}`} onClick={() => open(ref.current!)}>
    <img src={src} alt={alt} {...props} />
  </Button>;
}

export function PreviewImage(props: PreviewImageProps) {
  const gallery = useContext(ImagePreviewContext);
  return gallery ? <RegisteredImage {...props} /> : <ImagePreviewProvider><RegisteredImage {...props} /></ImagePreviewProvider>;
}
