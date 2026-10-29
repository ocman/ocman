import type { ReactNode } from 'react';
import { Tabs, TabsContent, TabsList, TabsTrigger } from './Tabs';
import './PickerTabs.css';

export interface PickerTabsConfig {
  label: string;
  value: string;
  items: { value: string; label: ReactNode }[];
  onChange: (value: string) => void;
}

/** Tabs that filter a picker's list; the list itself is the tab content. */
export function PickerTabs({ tabs, onChange, children }: { tabs?: PickerTabsConfig; onChange?: () => void; children: ReactNode }) {
  if (!tabs) return <>{children}</>;
  return (
    <Tabs className="oc-picker-tabs" value={tabs.value} onValueChange={(value) => {
      onChange?.();
      tabs.onChange(value);
    }}>
      <TabsList aria-label={tabs.label}>
        {tabs.items.map((tab) => <TabsTrigger key={tab.value} value={tab.value}>{tab.label}</TabsTrigger>)}
      </TabsList>
      <TabsContent value={tabs.value}>{children}</TabsContent>
    </Tabs>
  );
}
