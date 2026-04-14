import { useState, type ReactNode } from 'react';
import { HeaderContext, type HeaderInfo } from './headerContext';

export function HeaderProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<HeaderInfo>({});
  return (
    <HeaderContext.Provider value={{ info, setInfo }}>
      {children}
    </HeaderContext.Provider>
  );
}
