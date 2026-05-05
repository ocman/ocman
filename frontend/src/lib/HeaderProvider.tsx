import { useMemo, useState, type ReactNode } from 'react';
import { HeaderContext, type HeaderInfo } from './headerContext';

export function HeaderProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<HeaderInfo>({});
  const value = useMemo(() => ({ info, setInfo }), [info, setInfo]);
  return (
    <HeaderContext.Provider value={value}>
      {children}
    </HeaderContext.Provider>
  );
}
