import type { ComponentProps } from 'react';
import { Button } from './Control';

export type IconButtonProps = Omit<ComponentProps<typeof Button>, 'children' | 'aria-label'> & {
  label: string;
  icon: string;
};

export function IconButton({ label, icon, className = '', title = label, size = 'small', type = 'button', ...props }: IconButtonProps) {
  return (
    <Button {...props} type={type} size={size} title={title} aria-label={label} className={`oc-icon-button ${className}`}>
      <i className={`bi ${icon}`} aria-hidden="true" />
    </Button>
  );
}
