import { createContext, useContext, useEffect, useState, type ReactNode } from 'react';

export interface HeaderInfo {
  sessionTitle?: string;
  stats?: { label: string; value: string }[];
}

const HeaderContext = createContext<{
  info: HeaderInfo;
  setInfo: (info: HeaderInfo) => void;
}>({ info: {}, setInfo: () => {} });

export function HeaderProvider({ children }: { children: ReactNode }) {
  const [info, setInfo] = useState<HeaderInfo>({});
  return (
    <HeaderContext.Provider value={{ info, setInfo }}>
      {children}
    </HeaderContext.Provider>
  );
}

export function useHeaderInfo() {
  return useContext(HeaderContext);
}

export function usePageTitle(title: string) {
  useEffect(() => {
    document.title = title ? `${title} - ocman` : 'ocman';
  }, [title]);
}
