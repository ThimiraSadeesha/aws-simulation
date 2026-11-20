"use client";

import { useEffect, useMemo, useState } from "react";
import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, Loader2, RefreshCw, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import type { ListResponse } from "@/lib/types";
import { useListParams, type ListParamsApi } from "@/hooks/use-list-params";
import { useDebounced, useGlobalSearch } from "@/hooks/use-global-search";
import { EmptyState, ErrorState, SkeletonRows } from "./states";

export interface Column<T> {
  id: string;
  header: string;
  cell: (row: T) => React.ReactNode;
  /** Enables header sorting. Server mode sends `sort=<sortKey ?? id>`; client mode uses sortValue. */
  sortable?: boolean;
  sortKey?: string;
  sortValue?: (row: T) => string | number | boolean | null | undefined;
  className?: string;
  headerClassName?: string;
}

export interface FilterOption {
  value: string;
  label: string;
}

export interface FilterDef<T> {
  /** Query parameter name in server mode (e.g. `state`, `vpc_id`). */
  key: string;
  label: string;
  options: FilterOption[];
  /** Client mode predicate. Defaults to String(row[key]) === value. */
  predicate?: (row: T, value: string) => boolean;
}

export interface BulkAction<T> {
  label: string;
  icon?: React.ReactNode;
  destructive?: boolean;
  disabled?: (rows: T[]) => boolean;
  onClick: (rows: T[]) => void;
}

export interface ResourceTableProps<T> {
  /** Accessible name of the table. */
  label: string;
  columns: Column<T>[];
  getRowId: (row: T) => string;
  /** Server list response (server mode, requires `list`) or a plain array (client mode). */
  data: ListResponse<T> | T[] | undefined;
  /** Server-side mode: pass the list params state that drives the query. */
  list?: ListParamsApi;
  isLoading?: boolean;
  isFetching?: boolean;
  error?: unknown;
  onRetry?: () => void;
  /** Client mode: text used for free-text search. Default: JSON of the row. */
  searchText?: (row: T) => string;
  searchPlaceholder?: string;
  filters?: FilterDef<T>[];
  selectable?: boolean;
  bulkActions?: BulkAction<T>[];
  toolbar?: React.ReactNode;
  emptyTitle?: string;
  emptyDescription?: string;
  emptyAction?: React.ReactNode;
  onRowClick?: (row: T) => void;
  /** Whether the top-bar global search filters this table (default true). */
  globalSearch?: boolean;
  pageSizeOptions?: number[];
  /** Hide the search box (e.g. for tiny nested tables). */
  hideSearch?: boolean;
  className?: string;
  dense?: boolean;
}

function compareValues(a: unknown, b: unknown): number {
  if (a === b) return 0;
  if (a === null || a === undefined || a === "") return 1;
  if (b === null || b === undefined || b === "") return -1;
  if (typeof a === "number" && typeof b === "number") return a - b;
  if (typeof a === "boolean" && typeof b === "boolean") return a === b ? 0 : a ? -1 : 1;
  return String(a).localeCompare(String(b), undefined, { numeric: true, sensitivity: "base" });
}

export function ResourceTable<T>(props: ResourceTableProps<T>) {
  const {
    label,
    columns,
    getRowId,
    data,
    isLoading,
    isFetching,
    error,
    onRetry,
    searchText,
    searchPlaceholder,
    filters = [],
    selectable,
    bulkActions = [],
    toolbar,
    emptyTitle = "No resources",
    emptyDescription,
    emptyAction,
    onRowClick,
    globalSearch = true,
    pageSizeOptions = [10, 20, 50, 100],
    hideSearch,
    className,
    dense = true,
  } = props;

  const internal = useListParams({ pageSize: pageSizeOptions.includes(20) ? 20 : pageSizeOptions[0] });
  const serverMode = !!props.list;
  const list = props.list ?? internal;
  const { state } = list;

  // ---- search: local box OR the top-bar global search ----
  const global = useGlobalSearch();
  const [searchInput, setSearchInput] = useState(state.q);
  const effective = searchInput || (globalSearch ? global.query : "");
  const debounced = useDebounced(effective, serverMode ? 300 : 0);
  const { setQ } = list;
  useEffect(() => {
    setQ(debounced.trim());
  }, [debounced, setQ]);

  // ---- normalise rows ----
  const serverResponse = !Array.isArray(data) ? data : undefined;
  const allRows: T[] = useMemo(() => (Array.isArray(data) ? data : (data?.items ?? [])), [data]);

  const clientView = useMemo(() => {
    if (serverMode) return null;
    let rows = allRows;
    const q = state.q.toLowerCase();
    if (q) {
      rows = rows.filter((r) => (searchText ? searchText(r) : JSON.stringify(r)).toLowerCase().includes(q));
    }
    for (const f of filters) {
      const v = state.filters[f.key];
      if (!v) continue;
      rows = rows.filter((r) =>
        f.predicate ? f.predicate(r, v) : String((r as Record<string, unknown>)[f.key] ?? "") === v,
      );
    }
    if (state.sort) {
      const col = columns.find((c) => (c.sortKey ?? c.id) === state.sort);
      if (col) {
        const get = col.sortValue ?? ((r: T) => (r as Record<string, unknown>)[col.sortKey ?? col.id] as string);
        const dir = state.order === "desc" ? -1 : 1;
        rows = [...rows].sort((a, b) => dir * compareValues(get(a), get(b)));
      }
    }
    const total = rows.length;
    const start = (state.page - 1) * state.pageSize;
    return { rows: rows.slice(start, start + state.pageSize), total };
  }, [serverMode, allRows, state, filters, columns, searchText]);

  const rows = serverMode ? allRows : clientView!.rows;
  const total = serverMode ? (serverResponse?.total ?? allRows.length) : clientView!.total;
  const page = state.page;
  const pageSize = state.pageSize;
  const pageCount = Math.max(1, Math.ceil(total / pageSize));
  const from = total === 0 ? 0 : (page - 1) * pageSize + 1;
  const to = Math.min(total, (page - 1) * pageSize + rows.length);

  // ---- selection (kept to visible ids) ----
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const visibleIds = useMemo(() => rows.map(getRowId), [rows, getRowId]);
  const selectedRows = rows.filter((r) => selected.has(getRowId(r)));
  const allSelected = visibleIds.length > 0 && visibleIds.every((id) => selected.has(id));
  const someSelected = !allSelected && visibleIds.some((id) => selected.has(id));
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(visibleIds));
  const toggle = (id: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(id)) n.delete(id);
      else n.add(id);
      return n;
    });

  const onSort = (col: Column<T>) => {
    const key = col.sortKey ?? col.id;
    if (state.sort !== key) list.setSort(key, "asc");
    else if (state.order === "asc") list.setSort(key, "desc");
    else list.setSort(undefined);
  };

  const colCount = columns.length + (selectable ? 1 : 0);
  const hasActiveFilters = Object.keys(state.filters).length > 0 || !!state.q;

  return (
    <div className={cn("rounded-lg border bg-card", className)}>
      {/* Toolbar */}
      <div className="flex flex-wrap items-center gap-2 border-b p-2">
        {!hideSearch && (
          <div className="relative min-w-48 flex-1 sm:max-w-72">
            <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
            <Input
              type="search"
              aria-label={`Search ${label}`}
              placeholder={searchPlaceholder ?? "Search by name, ID or tag"}
              value={searchInput}
              onChange={(e) => setSearchInput(e.target.value)}
              className="h-8 pl-7 text-sm"
            />
          </div>
        )}
        {filters.map((f) => (
          <label key={f.key} className="flex items-center gap-1 text-xs text-muted-foreground">
            <span>{f.label}</span>
            <select
              aria-label={`Filter by ${f.label}`}
              className="h-8 rounded-md border bg-background px-2 text-sm text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
              value={state.filters[f.key] ?? ""}
              onChange={(e) => list.setFilter(f.key, e.target.value || undefined)}
            >
              <option value="">All</option>
              {f.options.map((o) => (
                <option key={o.value} value={o.value}>
                  {o.label}
                </option>
              ))}
            </select>
          </label>
        ))}
        {hasActiveFilters && (
          <Button
            size="sm"
            variant="ghost"
            onClick={() => {
              setSearchInput("");
              global.setQuery("");
              for (const k of Object.keys(state.filters)) list.setFilter(k, undefined);
            }}
          >
            <X /> Clear
          </Button>
        )}
        <div className="ml-auto flex flex-wrap items-center gap-2">
          {selectable && bulkActions.length > 0 && selectedRows.length > 0 && (
            <div className="flex items-center gap-2" role="group" aria-label="Bulk actions">
              <span className="text-xs text-muted-foreground">{selectedRows.length} selected</span>
              {bulkActions.map((a) => (
                <Button
                  key={a.label}
                  size="sm"
                  variant={a.destructive ? "destructive" : "outline"}
                  disabled={a.disabled?.(selectedRows)}
                  onClick={() => a.onClick(selectedRows)}
                >
                  {a.icon}
                  {a.label}
                </Button>
              ))}
            </div>
          )}
          {onRetry && (
            <Button size="icon-sm" variant="ghost" aria-label="Refresh" onClick={onRetry}>
              {isFetching ? <Loader2 className="animate-spin" /> : <RefreshCw />}
            </Button>
          )}
          {toolbar}
        </div>
      </div>

      {/* Table */}
      <div className="overflow-x-auto">
        <table className="w-full caption-bottom text-sm" aria-label={label} aria-busy={isLoading || isFetching || undefined}>
          <thead className="bg-muted/40">
            <tr className="border-b">
              {selectable && (
                <th scope="col" className="w-8 px-3 py-2">
                  <Checkbox
                    aria-label="Select all rows on this page"
                    checked={allSelected ? true : someSelected ? "indeterminate" : false}
                    onCheckedChange={toggleAll}
                  />
                </th>
              )}
              {columns.map((c) => {
                const key = c.sortKey ?? c.id;
                const active = state.sort === key;
                const ariaSort = active ? (state.order === "asc" ? "ascending" : "descending") : c.sortable ? "none" : undefined;
                return (
                  <th
                    key={c.id}
                    scope="col"
                    aria-sort={ariaSort}
                    className={cn("px-3 py-2 text-left text-xs font-semibold whitespace-nowrap text-muted-foreground", c.headerClassName)}
                  >
                    {c.sortable ? (
                      <button
                        type="button"
                        onClick={() => onSort(c)}
                        className="-mx-1 inline-flex items-center gap-1 rounded px-1 hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring/50 focus-visible:outline-none"
                      >
                        {c.header}
                        {active ? (
                          state.order === "asc" ? (
                            <ArrowUp className="size-3" aria-hidden />
                          ) : (
                            <ArrowDown className="size-3" aria-hidden />
                          )
                        ) : (
                          <ArrowUpDown className="size-3 opacity-40" aria-hidden />
                        )}
                      </button>
                    ) : (
                      c.header
                    )}
                  </th>
                );
              })}
            </tr>
          </thead>
          <tbody>
            {isLoading ? (
              <SkeletonRows cols={colCount} />
            ) : error ? (
              <tr>
                <td colSpan={colCount}>
                  <ErrorState error={error} onRetry={onRetry} />
                </td>
              </tr>
            ) : rows.length === 0 ? (
              <tr>
                <td colSpan={colCount}>
                  {hasActiveFilters ? (
                    <EmptyState title="No matching resources" description="Try a different search or clear the filters." />
                  ) : (
                    <EmptyState title={emptyTitle} description={emptyDescription} action={emptyAction} />
                  )}
                </td>
              </tr>
            ) : (
              rows.map((row) => {
                const id = getRowId(row);
                const isSel = selected.has(id);
                return (
                  <tr
                    key={id}
                    data-state={isSel ? "selected" : undefined}
                    className={cn(
                      "border-b transition-colors last:border-b-0 hover:bg-muted/40 data-[state=selected]:bg-primary/5",
                      onRowClick && "cursor-pointer",
                    )}
                    onClick={onRowClick ? () => onRowClick(row) : undefined}
                  >
                    {selectable && (
                      <td className="w-8 px-3 py-1.5" onClick={(e) => e.stopPropagation()}>
                        <Checkbox aria-label={`Select ${id}`} checked={isSel} onCheckedChange={() => toggle(id)} />
                      </td>
                    )}
                    {columns.map((c) => (
                      <td key={c.id} className={cn("px-3 align-middle", dense ? "py-1.5" : "py-2.5", c.className)}>
                        {c.cell(row)}
                      </td>
                    ))}
                  </tr>
                );
              })
            )}
          </tbody>
        </table>
      </div>

      {/* Pagination */}
      <div className="flex flex-wrap items-center justify-between gap-2 border-t px-3 py-2 text-xs text-muted-foreground">
        <span aria-live="polite">
          {total === 0 ? "0 items" : `Showing ${from}–${to} of ${total}`}
        </span>
        <div className="flex items-center gap-2">
          <label className="flex items-center gap-1">
            <span>Rows</span>
            <select
              aria-label="Rows per page"
              className="h-7 rounded-md border bg-background px-1 text-xs text-foreground"
              value={pageSize}
              onChange={(e) => list.setPageSize(Number(e.target.value))}
            >
              {pageSizeOptions.map((n) => (
                <option key={n} value={n}>
                  {n}
                </option>
              ))}
            </select>
          </label>
          <Button
            size="icon-sm"
            variant="outline"
            aria-label="Previous page"
            disabled={page <= 1}
            onClick={() => list.setPage(page - 1)}
          >
            <ChevronLeft />
          </Button>
          <span>
            Page {page} of {pageCount}
          </span>
          <Button
            size="icon-sm"
            variant="outline"
            aria-label="Next page"
            disabled={page >= pageCount}
            onClick={() => list.setPage(page + 1)}
          >
            <ChevronRight />
          </Button>
        </div>
      </div>
    </div>
  );
}
