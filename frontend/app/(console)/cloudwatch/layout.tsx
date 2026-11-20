"use client";

import { ServiceNav } from "@/components/shell/service-nav";

export default function CloudWatchLayout({ children }: { children: React.ReactNode }) {
  return (
    <div>
      <ServiceNav
        label="CloudWatch"
        items={[
          { label: "Metrics", href: "/cloudwatch", exact: true },
          { label: "Alarms", href: "/cloudwatch/alarms" },
          { label: "Log groups", href: "/cloudwatch/logs" },
          { label: "Events", href: "/cloudwatch/events" },
        ]}
      />
      {children}
    </div>
  );
}
