"use client";

import { useParams } from "next/navigation";
import { PageHeader } from "@/components/console/page-header";
import { EmptyState, ErrorState, LoadingState } from "@/components/console/states";
import { useTable } from "@/features/dynamodb/api";
import { TableDetail } from "@/features/dynamodb/table-detail";

export default function DynamoTablePage() {
  const params = useParams<{ table: string }>();
  const name = decodeURIComponent(params.table);
  const q = useTable(name);
  return (
    <div>
      <PageHeader breadcrumbs={[{ label: "DynamoDB", href: "/dynamodb" }, { label: "Tables", href: "/dynamodb" }, { label: name }]} title={name} />
      {q.isLoading ? (
        <LoadingState />
      ) : q.isError ? (
        <ErrorState error={q.error} onRetry={() => void q.refetch()} />
      ) : q.data ? (
        <TableDetail table={q.data} />
      ) : (
        <EmptyState title="Table not found" description={`No table named ${name} exists in this account.`} />
      )}
    </div>
  );
}
