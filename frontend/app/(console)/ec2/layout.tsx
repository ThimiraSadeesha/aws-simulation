import { ServiceNav } from "@/components/shell/service-nav";

const ITEMS = [
  { label: "Instances", href: "/ec2", exact: true },
  { label: "Launch instances", href: "/ec2/launch" },
  { label: "Key pairs", href: "/ec2/key-pairs" },
  { label: "Instance types", href: "/ec2/instance-types" },
  { label: "Images (AMIs)", href: "/ec2/images" },
];

export default function Ec2Layout({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <ServiceNav label="EC2" items={ITEMS} />
      {children}
    </div>
  );
}
