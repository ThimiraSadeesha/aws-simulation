"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { DOCS } from "@/features/docs/content";
import { cn } from "@/lib/utils";

export default function DocsLayout({ children }: { children: React.ReactNode }) {
  const pathname = usePathname() ?? "";
  return (
    <div className="grid gap-6 lg:grid-cols-[14rem_1fr]">
      <nav aria-label="Documentation" className="lg:sticky lg:top-16 lg:self-start">
        <h2 className="mb-2 px-2 text-xs font-semibold tracking-wide text-muted-foreground uppercase">User guide</h2>
        <ul className="grid gap-0.5">
          {DOCS.map((d) => {
            const href = `/docs/${d.slug}`;
            const active = pathname === href || (pathname === "/docs" && d.slug === "getting-started");
            return (
              <li key={d.slug}>
                <Link
                  href={href}
                  aria-current={active ? "page" : undefined}
                  className={cn(
                    "block rounded-md px-2 py-1.5 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
                    active ? "bg-primary/10 font-medium" : "text-muted-foreground hover:bg-muted hover:text-foreground",
                  )}
                >
                  {d.title}
                </Link>
              </li>
            );
          })}
        </ul>
      </nav>
      <div className="min-w-0">{children}</div>
    </div>
  );
}
