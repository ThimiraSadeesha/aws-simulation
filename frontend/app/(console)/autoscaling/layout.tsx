"use client";

import { ServiceNav } from "@/components/shell/service-nav";

export default function AutoScalingLayout({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <ServiceNav
        label="Auto Scaling"
        items={[
          { label: "Auto Scaling groups", href: "/autoscaling", exact: true },
          { label: "Launch templates", href: "/autoscaling/launch-templates" },
        ]}
      />
      {children}
    </div>
  );
}
