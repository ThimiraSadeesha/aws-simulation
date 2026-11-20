import { ServiceNav } from "@/components/shell/service-nav";

const ITEMS = [
  { label: "Overview", href: "/iam", exact: true },
  { label: "Users", href: "/iam/users" },
  { label: "Groups", href: "/iam/groups" },
  { label: "Roles", href: "/iam/roles" },
  { label: "Policies", href: "/iam/policies" },
  { label: "Policy simulator", href: "/iam/simulator" },
];

export default function IamLayout({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <ServiceNav label="IAM" items={ITEMS} />
      {children}
    </div>
  );
}
