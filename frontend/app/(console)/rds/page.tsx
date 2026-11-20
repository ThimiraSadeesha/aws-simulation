import { NotAvailable } from "@/components/console/not-available";

export const metadata = { title: "RDS" };

export default function Page() {
  return (
    <NotAvailable
      service="RDS"
      summary="Managed relational databases."
      planned={[
        "DB instances with the creating → available → stopping/stopped lifecycle already modelled by the simulation engine (db_instance kind).",
        "DB subnet groups placed in your simulated VPC subnets, guarded by security groups.",
        "Simulated endpoints, snapshots and CloudWatch metrics (CPU, connections, free storage).",
      ]}
    />
  );
}
