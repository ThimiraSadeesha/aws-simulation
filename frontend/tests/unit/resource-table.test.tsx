import "../next-navigation.mock";
import { useEffect } from "react";
import { act, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { http, HttpResponse } from "msw";
import { describe, expect, it, vi } from "vitest";
import { useQuery, keepPreviousData } from "@tanstack/react-query";
import { ResourceTable, type Column } from "@/components/console/resource-table";
import { StatusBadge } from "@/components/console/status-badge";
import { useListParams } from "@/hooks/use-list-params";
import { GlobalSearchProvider, useGlobalSearch } from "@/hooks/use-global-search";
import { apiList } from "@/lib/api/client";
import { server } from "../msw/server";
import { renderWithProviders } from "../utils";

interface Row {
  id: string;
  name: string;
  state: string;
  cpu: number;
}

const rows: Row[] = Array.from({ length: 25 }, (_, i) => ({
  id: `i-${String(i).padStart(3, "0")}`,
  name: `web-${String.fromCharCode(97 + (i % 26))}${i}`,
  state: i % 3 === 0 ? "stopped" : "running",
  cpu: (i * 7) % 100,
}));

const columns: Column<Row>[] = [
  { id: "id", header: "ID", sortable: true, cell: (r) => r.id },
  { id: "name", header: "Name", sortable: true, cell: (r) => r.name },
  { id: "state", header: "State", cell: (r) => <StatusBadge status={r.state} /> },
  { id: "cpu", header: "CPU", sortable: true, cell: (r) => `${r.cpu}%` },
];

function bodyRows() {
  const table = screen.getByRole("table", { name: "Instances" });
  return within(table).getAllByRole("row").slice(1);
}
function firstCellTexts() {
  return bodyRows().map((r) => within(r).getAllByRole("cell")[0].textContent);
}

describe("ResourceTable (client mode)", () => {
  it("paginates 20 per page and navigates pages", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ResourceTable label="Instances" columns={columns} getRowId={(r) => r.id} data={rows} />);
    expect(bodyRows()).toHaveLength(20);
    expect(screen.getByText("Showing 1–20 of 25")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Previous page" })).toBeDisabled();
    await user.click(screen.getByRole("button", { name: "Next page" }));
    expect(bodyRows()).toHaveLength(5);
    expect(screen.getByText("Showing 21–25 of 25")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Next page" })).toBeDisabled();
    await user.selectOptions(screen.getByLabelText("Rows per page"), "10");
    expect(bodyRows()).toHaveLength(10);
    expect(screen.getByText("Page 1 of 3")).toBeInTheDocument();
  });

  it("sorts by column ascending, descending, then clears", async () => {
    const user = userEvent.setup();
    renderWithProviders(<ResourceTable label="Instances" columns={columns} getRowId={(r) => r.id} data={rows} pageSizeOptions={[50]} />);
    const cpuHeader = screen.getByRole("columnheader", { name: /CPU/ });
    expect(cpuHeader).toHaveAttribute("aria-sort", "none");
    await user.click(within(cpuHeader).getByRole("button"));
    expect(cpuHeader).toHaveAttribute("aria-sort", "ascending");
    const asc = bodyRows().map((r) => Number(within(r).getAllByRole("cell")[3].textContent!.replace("%", "")));
    expect(asc).toEqual([...asc].sort((a, b) => a - b));
    await user.click(within(cpuHeader).getByRole("button"));
    expect(cpuHeader).toHaveAttribute("aria-sort", "descending");
    const desc = bodyRows().map((r) => Number(within(r).getAllByRole("cell")[3].textContent!.replace("%", "")));
    expect(desc).toEqual([...desc].sort((a, b) => b - a));
    await user.click(within(cpuHeader).getByRole("button"));
    expect(cpuHeader).toHaveAttribute("aria-sort", "none");
    expect(firstCellTexts()[0]).toBe("i-000");
  });

  it("filters with a select filter and free-text search, showing an empty state", async () => {
    const user = userEvent.setup();
    renderWithProviders(
      <ResourceTable
        label="Instances"
        columns={columns}
        getRowId={(r) => r.id}
        data={rows}
        pageSizeOptions={[50]}
        filters={[{ key: "state", label: "State", options: [{ value: "running", label: "running" }, { value: "stopped", label: "stopped" }] }]}
      />,
    );
    await user.selectOptions(screen.getByLabelText("Filter by State"), "stopped");
    expect(bodyRows()).toHaveLength(9);
    bodyRows().forEach((r) => expect(r).toHaveTextContent("stopped"));
    await user.type(screen.getByLabelText("Search Instances"), "i-003");
    await waitFor(() => expect(bodyRows()).toHaveLength(1));
    await user.clear(screen.getByLabelText("Search Instances"));
    await user.type(screen.getByLabelText("Search Instances"), "nothing-matches");
    await waitFor(() => expect(screen.getByText("No matching resources")).toBeInTheDocument());
    await user.click(screen.getByRole("button", { name: /Clear/ }));
    await waitFor(() => expect(bodyRows()).toHaveLength(25));
  });

  it("supports row selection and bulk actions", async () => {
    const user = userEvent.setup();
    const onStop = vi.fn();
    renderWithProviders(
      <ResourceTable
        label="Instances"
        columns={columns}
        getRowId={(r) => r.id}
        data={rows.slice(0, 5)}
        selectable
        bulkActions={[{ label: "Stop", onClick: onStop, disabled: (sel) => sel.some((r) => r.state === "stopped") }]}
      />,
    );
    expect(screen.queryByRole("group", { name: "Bulk actions" })).not.toBeInTheDocument();
    await user.click(screen.getByRole("checkbox", { name: "Select i-001" }));
    await user.click(screen.getByRole("checkbox", { name: "Select i-002" }));
    const group = screen.getByRole("group", { name: "Bulk actions" });
    expect(within(group).getByText("2 selected")).toBeInTheDocument();
    await user.click(within(group).getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalledWith([rows[1], rows[2]]);
    await user.click(screen.getByRole("checkbox", { name: "Select all rows on this page" }));
    expect(within(screen.getByRole("group", { name: "Bulk actions" })).getByText("5 selected")).toBeInTheDocument();
    // i-000 is stopped → action disabled
    expect(within(screen.getByRole("group", { name: "Bulk actions" })).getByRole("button", { name: "Stop" })).toBeDisabled();
  });

  it("renders loading, error and empty states", async () => {
    const onRetry = vi.fn();
    const { rerender } = renderWithProviders(<ResourceTable label="Instances" columns={columns} getRowId={(r) => r.id} data={undefined} isLoading />);
    expect(screen.getByRole("table", { name: "Instances" })).toHaveAttribute("aria-busy", "true");
    rerender(<ResourceTable label="Instances" columns={columns} getRowId={(r) => r.id} data={undefined} error={new Error("boom")} onRetry={onRetry} />);
    expect(screen.getByRole("alert")).toHaveTextContent("boom");
    await userEvent.click(within(screen.getByRole("alert")).getByRole("button", { name: /Retry/ }));
    expect(onRetry).toHaveBeenCalled();
    rerender(<ResourceTable label="Instances" columns={columns} getRowId={(r) => r.id} data={[]} emptyTitle="No instances yet" />);
    expect(screen.getByText("No instances yet")).toBeInTheDocument();
  });
});

describe("ResourceTable (server mode)", () => {
  function ServerTable() {
    const list = useListParams();
    const q = useQuery({
      queryKey: ["test", list.params],
      queryFn: () => apiList<Row>("/ec2/instances", list.params),
      placeholderData: keepPreviousData,
    });
    return (
      <ResourceTable
        label="Instances"
        columns={columns}
        getRowId={(r) => r.id}
        list={list}
        data={q.data}
        isLoading={q.isLoading}
        error={q.error}
        filters={[{ key: "state", label: "State", options: [{ value: "running", label: "running" }] }]}
      />
    );
  }

  it("sends sort, order, page, page_size, q and filters to the API", async () => {
    const requests: URLSearchParams[] = [];
    server.use(
      http.get("*/api/v1/ec2/instances", ({ request }) => {
        const sp = new URL(request.url).searchParams;
        requests.push(sp);
        const page = Number(sp.get("page") ?? 1);
        const size = Number(sp.get("page_size") ?? 20);
        return HttpResponse.json({ items: rows.slice((page - 1) * size, page * size), total: 45, page, page_size: size });
      }),
    );
    const user = userEvent.setup();
    renderWithProviders(<ServerTable />);
    await waitFor(() => expect(screen.getByText("Showing 1–20 of 45")).toBeInTheDocument());
    expect(requests.at(-1)!.get("page")).toBe("1");
    expect(requests.at(-1)!.get("page_size")).toBe("20");

    await user.click(within(screen.getByRole("columnheader", { name: /Name/ })).getByRole("button"));
    await waitFor(() => expect(requests.at(-1)!.get("sort")).toBe("name"));
    expect(requests.at(-1)!.get("order")).toBe("asc");
    await user.click(within(screen.getByRole("columnheader", { name: /Name/ })).getByRole("button"));
    await waitFor(() => expect(requests.at(-1)!.get("order")).toBe("desc"));

    await user.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => expect(requests.at(-1)!.get("page")).toBe("2"));
    await waitFor(() => expect(screen.getByText("Showing 21–25 of 45")).toBeInTheDocument());
    expect(screen.getByText("Page 2 of 3")).toBeInTheDocument();

    await user.selectOptions(screen.getByLabelText("Filter by State"), "running");
    await waitFor(() => expect(requests.at(-1)!.get("state")).toBe("running"));
    expect(requests.at(-1)!.get("page")).toBe("1"); // filters reset to page 1

    await user.type(screen.getByLabelText("Search Instances"), "web");
    await waitFor(() => expect(requests.at(-1)!.get("q")).toBe("web"), { timeout: 2000 });
  });

  it("uses the top-bar global search when the table search is empty", async () => {
    const requests: URLSearchParams[] = [];
    server.use(
      http.get("*/api/v1/ec2/instances", ({ request }) => {
        requests.push(new URL(request.url).searchParams);
        return HttpResponse.json({ items: [], total: 0, page: 1, page_size: 20 });
      }),
    );
    let setGlobal: (q: string) => void = () => {};
    function Grab() {
      const g = useGlobalSearch();
      useEffect(() => {
        setGlobal = g.setQuery;
      }, [g.setQuery]);
      return null;
    }
    renderWithProviders(
      <GlobalSearchProvider>
        <Grab />
        <ServerTable />
      </GlobalSearchProvider>,
    );
    await waitFor(() => expect(requests.length).toBeGreaterThan(0));
    act(() => setGlobal("db-"));
    await waitFor(() => expect(requests.at(-1)!.get("q")).toBe("db-"), { timeout: 2000 });
  });
});
