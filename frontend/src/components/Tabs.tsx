import type { ComponentProps } from 'react';
import * as Primitive from '@radix-ui/react-tabs';
import './Tabs.css';

// Manual activation keeps arrow-key navigation from starting requests or
// mounting expensive panels before the user selects a tab.
export function Tabs({ activationMode = 'manual', ...props }: ComponentProps<typeof Primitive.Root>) {
  return <Primitive.Root {...props} activationMode={activationMode} />;
}

export function TabsList({ className = '', ...props }: ComponentProps<typeof Primitive.List> & { 'aria-label': string }) {
  return <Primitive.List {...props} className={`oc-tabs-list ${className}`} />;
}

export function TabsTrigger({ className = '', ...props }: ComponentProps<typeof Primitive.Trigger>) {
  return <Primitive.Trigger {...props} className={`oc-tabs-trigger ${className}`} />;
}

export function TabsContent({ className = '', ...props }: ComponentProps<typeof Primitive.Content>) {
  return <Primitive.Content {...props} className={`oc-tabs-content ${className}`} />;
}
