"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { cn } from "@/lib/utils";

export interface ServiceNavItem {
  label: string;
  href: string;
  /** Match only the exact path (for index pages). */
  exact?: boolean;
}

/** Secondary navigation inside a service (e.g. VPC → Subnets, Route tables…). */
export function ServiceNav({ label, items }: { label: string; items: ServiceNavItem[] }) {
  const pathname = usePathname() ?? "";
  return (
    <nav aria-label={`${label} navigation`} className="-mx-1 mb-4 overflow-x-auto border-b">
      <ul className="flex min-w-max gap-1 px-1">
        {items.map((i) => {
          const active = i.exact ? pathname === i.href : pathname === i.href || pathname.startsWith(`${i.href}/`);
          return (
            <li key={i.href}>
              <Link
                href={i.href}
                aria-current={active ? "page" : undefined}
                className={cn(
                  "-mb-px inline-block border-b-2 px-2.5 py-2 text-sm whitespace-nowrap outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
                  active ? "border-sky-600 font-medium text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
                )}
              >
                {i.label}
              </Link>
            </li>
          );
        })}
      </ul>
    </nav>
  );
}
