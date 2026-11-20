"use client";

import { useCallback, useMemo, useState } from "react";
import type { ListParams } from "@/lib/types";

export interface ListState {
  q: string;
  sort?: string;
  order: "asc" | "desc";
  page: number;
  pageSize: number;
  filters: Record<string, string>;
}

export interface ListParamsApi {
  state: ListState;
  /** Wire params for the list endpoint (`q,sort,order,page,page_size` + filters). */
  params: ListParams;
  setQ: (q: string) => void;
  setSort: (sort: string | undefined, order?: "asc" | "desc") => void;
  setPage: (page: number) => void;
  setPageSize: (size: number) => void;
  setFilter: (key: string, value: string | undefined) => void;
  reset: () => void;
}

export function toWireParams(s: ListState): ListParams {
  const p: ListParams = { page: s.page, page_size: s.pageSize };
  if (s.q) p.q = s.q;
  if (s.sort) {
    p.sort = s.sort;
    p.order = s.order;
  }
  for (const [k, v] of Object.entries(s.filters)) if (v) p[k] = v;
  return p;
}

export function useListParams(initial: Partial<ListState> = {}): ListParamsApi {
  const [init] = useState<ListState>(() => ({
    q: "",
    order: "asc",
    page: 1,
    pageSize: 20,
    filters: {},
    ...initial,
  }));
  const [state, setState] = useState<ListState>(init);

  const setQ = useCallback((q: string) => setState((s) => (s.q === q ? s : { ...s, q, page: 1 })), []);
  const setSort = useCallback(
    (sort: string | undefined, order: "asc" | "desc" = "asc") => setState((s) => ({ ...s, sort, order, page: 1 })),
    [],
  );
  const setPage = useCallback((page: number) => setState((s) => ({ ...s, page })), []);
  const setPageSize = useCallback((pageSize: number) => setState((s) => ({ ...s, pageSize, page: 1 })), []);
  const setFilter = useCallback(
    (key: string, value: string | undefined) =>
      setState((s) => {
        const filters = { ...s.filters };
        if (value) filters[key] = value;
        else delete filters[key];
        return { ...s, filters, page: 1 };
      }),
    [],
  );
  const reset = useCallback(() => setState(init), [init]);
  const params = useMemo(() => toWireParams(state), [state]);
  return { state, params, setQ, setSort, setPage, setPageSize, setFilter, reset };
}
