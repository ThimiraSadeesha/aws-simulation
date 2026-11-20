"use client";

import { createContext, useContext, useEffect, useState } from "react";
import { usePathname } from "next/navigation";

interface GlobalSearch {
  query: string;
  setQuery: (q: string) => void;
}

const Ctx = createContext<GlobalSearch>({ query: "", setQuery: () => {} });

/** The top-bar search box filters whichever ResourceTable is on the current page. Reset on navigation. */
export function GlobalSearchProvider({ children }: { children: React.ReactNode }) {
  const [query, setQuery] = useState("");
  const pathname = usePathname();
  const [lastPath, setLastPath] = useState(pathname);
  if (lastPath !== pathname) {
    setLastPath(pathname);
    setQuery("");
  }
  return <Ctx.Provider value={{ query, setQuery }}>{children}</Ctx.Provider>;
}

export function useGlobalSearch() {
  return useContext(Ctx);
}

/** Debounce a value. */
export function useDebounced<T>(value: T, ms = 300): T {
  const [v, setV] = useState(value);
  useEffect(() => {
    const t = setTimeout(() => setV(value), ms);
    return () => clearTimeout(t);
  }, [value, ms]);
  return v;
}
