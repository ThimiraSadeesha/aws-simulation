import { NotAvailable } from "@/components/console/not-available";

export const metadata = { title: "CloudFormation" };

export default function Page() {
  return (
    <NotAvailable
      service="CloudFormation"
      summary="Infrastructure as code stacks."
      planned={[
        "Stacks created from JSON/YAML templates describing VPCs, subnets, security groups, instances and buckets.",
        "Change sets, stack events and rollback on failure, driven through the same APIs the console uses.",
        "Drift detection against the simulator state.",
      ]}
    />
  );
}
