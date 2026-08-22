import type { ReactNode } from 'react';
import { resolveModelLogo } from '../lib/modelLogos';
import './ModelLogo.css';

export function ModelLogo({ model, muted = false }: { model: string; muted?: boolean }) {
  const logo = resolveModelLogo(model);
  return <img className={`oc-model-logo${logo.color && !muted ? '' : ' oc-model-logo--mono'}${muted ? ' oc-model-logo--muted' : ''}`} src={logo.src} alt="" aria-hidden="true" title={logo.name} />;
}

export function ModelLabel({ model, muted, children }: { model: string; muted?: boolean; children: ReactNode }) {
  return <span className="oc-model-label"><ModelLogo model={model} muted={muted} />{children}</span>;
}
