import { createContext, useContext, useEffect } from 'react';

export interface HeaderInfo {
  sessionTitle?: string;
  stats?: { label: string; value: string }[];
}

export const HeaderContext = createContext<{
  info: HeaderInfo;
  setInfo: (info: HeaderInfo) => void;
}>({ info: {}, setInfo: () => {} });

export function useHeaderInfo() {
  return useContext(HeaderContext);
}

export function usePageTitle(title: string) {
  useEffect(() => {
    document.title = title ? `${title} - ocman` : 'ocman';
  }, [title]);
}
