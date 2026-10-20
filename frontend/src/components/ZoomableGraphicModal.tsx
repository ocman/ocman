import { useState } from 'react';
import type { ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { TransformComponent, TransformWrapper } from 'react-zoom-pan-pinch';
import { Modal } from './Modal';
import { IconButton } from './IconButton';

export function ZoomableGraphicModal({ label, closeLabel, maxScale = 4, onClose, children, navigation }: {
  label: string;
  closeLabel: string;
  maxScale?: number;
  onClose: () => void;
  children: ReactNode;
  navigation?: ReactNode;
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
              {navigation}
              <IconButton label="Zoom out" icon="bi-dash-lg" variant="ghost" onClick={() => zoomOut(0.25, 0)} />
              <span>{Math.round(zoom * 100)}%</span>
              <IconButton label="Zoom in" icon="bi-plus-lg" variant="ghost" onClick={() => zoomIn(0.25, 0)} />
              <IconButton label={closeLabel} icon="bi-x-lg" variant="ghost" onClick={onClose} />
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
