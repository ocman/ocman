import type { ReactNode } from 'react';
import { resolveModelLogo } from '../lib/modelLogos';
import './ModelLogo.css';

export function ModelLogo({ model }: { model: string }) {
  const logo = resolveModelLogo(model);
  return <img className={`oc-model-logo${logo.color ? '' : ' oc-model-logo--mono'}`} src={logo.src} alt="" aria-hidden="true" title={logo.name} />;
}

export function ModelLabel({ model, children }: { model: string; children: ReactNode }) {
  return <span className="oc-model-label"><ModelLogo model={model} />{children}</span>;
}
