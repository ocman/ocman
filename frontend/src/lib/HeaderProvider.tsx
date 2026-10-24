import { useState, type ReactNode } from 'react';
import { HeaderContext, type HeaderInfo } from './headerContext';

export function HeaderProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<HeaderInfo>({});
  // eslint-disable-next-line react/jsx-no-constructed-context-values -- React Compiler memoizes value.
  const value = { info, setInfo };
  return (
    <HeaderContext.Provider value={value}>
      {children}
    </HeaderContext.Provider>
  );
}
