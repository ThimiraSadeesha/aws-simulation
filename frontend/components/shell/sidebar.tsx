"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Cloud } from "lucide-react";
import { cn } from "@/lib/utils";
import { NAV_ITEMS, isActive, type NavItem } from "./nav-items";

const GROUP_LABEL: Record<NavItem["group"], string> = {
  overview: "Overview",
  services: "Services",
  learn: "Learn",
  account: "Account",
};

export function SidebarNav({ onNavigate }: { onNavigate?: () => void }) {
  const pathname = usePathname() ?? "/";
  const groups = (["overview", "services", "learn", "account"] as const).map((g) => ({
    group: g,
    items: NAV_ITEMS.filter((i) => i.group === g),
  }));
  return (
    <nav aria-label="Main navigation" className="flex flex-col gap-3 px-2 py-3">
      {groups.map(({ group, items }) => (
        <div key={group}>
          <div className="px-2 pb-1 text-[11px] font-semibold tracking-wide text-muted-foreground uppercase">{GROUP_LABEL[group]}</div>
          <ul className="grid gap-0.5">
            {items.map((item) => {
              const active = isActive(pathname, item.href);
              const Icon = item.icon;
              return (
                <li key={item.href}>
                  <Link
                    href={item.href}
                    onClick={onNavigate}
                    aria-current={active ? "page" : undefined}
                    className={cn(
                      "flex items-center gap-2 rounded-md px-2 py-1.5 text-sm transition-colors outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
                      active ? "bg-primary/10 font-medium text-foreground" : "text-muted-foreground hover:bg-muted hover:text-foreground",
                    )}
                  >
                    <Icon className={cn("size-4 shrink-0", active && "text-sky-600 dark:text-sky-400")} aria-hidden />
                    <span className="truncate">{item.label}</span>
                    {item.unavailable && (
                      <span className="ml-auto rounded bg-muted px-1 text-[10px] text-muted-foreground" title="Not yet available in this simulator release">
                        soon
                      </span>
                    )}
                  </Link>
                </li>
              );
            })}
          </ul>
        </div>
      ))}
    </nav>
  );
}

export function Brand() {
  return (
    <Link href="/dashboard" className="flex items-center gap-2 rounded-md px-1 font-semibold tracking-tight outline-none focus-visible:ring-2 focus-visible:ring-ring/60">
      <span className="flex size-6 items-center justify-center rounded-md bg-sky-600 text-white">
        <Cloud className="size-3.5" aria-hidden />
      </span>
      <span className="text-sm">CloudSim Console</span>
    </Link>
  );
}

export function Sidebar() {
  return (
    <aside className="sticky top-0 hidden h-screen w-56 shrink-0 flex-col border-r bg-sidebar lg:flex">
      <div className="flex h-12 items-center border-b px-3">
        <Brand />
      </div>
      <div className="flex-1 overflow-y-auto">
        <SidebarNav />
      </div>
      <div className="border-t p-3 text-[11px] leading-snug text-muted-foreground">
        Educational simulator. Service names are generic labels; no affiliation with any cloud provider.
      </div>
    </aside>
  );
}
