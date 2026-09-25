import type { ComponentProps } from 'react';
import * as Primitive from '@radix-ui/react-dropdown-menu';
import './DropdownMenu.css';

export function DropdownMenu(props: ComponentProps<typeof Primitive.Root>) {
  return <Primitive.Root {...props} />;
}

export function DropdownMenuTrigger(props: ComponentProps<typeof Primitive.Trigger>) {
  return <Primitive.Trigger {...props} />;
}

export function DropdownMenuContent({ className = '', sideOffset = 4, align = 'end', ...props }: ComponentProps<typeof Primitive.Content>) {
  return (
    <Primitive.Portal>
      <Primitive.Content {...props} align={align} sideOffset={sideOffset} className={`oc-dropdown-menu ${className}`} />
    </Primitive.Portal>
  );
}

export function DropdownMenuItem({ className = '', ...props }: ComponentProps<typeof Primitive.Item>) {
  return <Primitive.Item {...props} className={`oc-dropdown-menu-item ${className}`} />;
}

export function DropdownMenuSeparator({ className = '', ...props }: ComponentProps<typeof Primitive.Separator>) {
  return <Primitive.Separator {...props} className={`oc-dropdown-menu-separator ${className}`} />;
}
