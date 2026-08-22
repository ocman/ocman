import type { ReactNode } from 'react';
import { resolveModelLogo } from '../lib/modelLogos';
import './ModelLogo.css';

export function ModelLogo({ model, monochrome = false }: { model: string; monochrome?: boolean }) {
  const logo = resolveModelLogo(model);
  return <img className={`oc-model-logo${logo.color && !monochrome ? '' : ' oc-model-logo--mono'}`} src={logo.src} alt="" aria-hidden="true" title={logo.name} />;
}

export function ModelLabel({ model, monochrome, children }: { model: string; monochrome?: boolean; children: ReactNode }) {
  return <span className="oc-model-label"><ModelLogo model={model} monochrome={monochrome} />{children}</span>;
}
