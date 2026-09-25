import type { ReactNode } from 'react';
import { ButtonGroup } from './Control';
import './ModalFooter.css';

export function ModalFooter({ label, children }: { label: string; children: ReactNode }) {
  return <ButtonGroup label={label} className="oc-modal-footer">{children}</ButtonGroup>;
}
