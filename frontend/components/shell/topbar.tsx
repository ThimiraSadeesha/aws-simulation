"use client";

import Link from "next/link";
import { FlaskConical, LogOut, Menu, Monitor, Moon, Search, Settings, Sun, User as UserIcon } from "lucide-react";
import { useTheme } from "next-themes";
import { useState } from "react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Sheet, SheetContent, SheetHeader, SheetTitle } from "@/components/ui/sheet";
import { Tooltip, TooltipContent, TooltipTrigger } from "@/components/ui/tooltip";
import { CopyButton } from "@/components/console/copy-button";
import { useAuth } from "@/features/auth/auth-context";
import { useGlobalSearch } from "@/hooks/use-global-search";
import { Brand, SidebarNav } from "./sidebar";
import { LiveIndicator } from "./live-indicator";

export function SimulatedBadge() {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <span
          tabIndex={0}
          className="hidden items-center gap-1 rounded-full border border-amber-500/40 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium whitespace-nowrap text-amber-800 outline-none focus-visible:ring-2 focus-visible:ring-ring/60 md:inline-flex dark:text-amber-300"
          data-testid="simulated-indicator"
        >
          <FlaskConical className="size-3" aria-hidden /> Simulated environment — no real cloud resources
        </span>
      </TooltipTrigger>
      <TooltipContent>Everything here runs locally in the CloudSim simulator. Nothing is created in a real cloud account.</TooltipContent>
    </Tooltip>
  );
}

export function Topbar() {
  const { user, logout } = useAuth();
  const { query, setQuery } = useGlobalSearch();
  const { theme, setTheme } = useTheme();
  const [mobileOpen, setMobileOpen] = useState(false);

  return (
    <header className="sticky top-0 z-30 flex h-12 items-center gap-2 border-b bg-background/95 px-3 backdrop-blur supports-[backdrop-filter]:bg-background/80">
      <Button variant="ghost" size="icon-sm" className="lg:hidden" aria-label="Open navigation" onClick={() => setMobileOpen(true)}>
        <Menu />
      </Button>
      <Sheet open={mobileOpen} onOpenChange={setMobileOpen}>
        <SheetContent side="left" className="w-64 p-0">
          <SheetHeader className="border-b">
            <SheetTitle asChild>
              <div>
                <Brand />
              </div>
            </SheetTitle>
          </SheetHeader>
          <div className="overflow-y-auto">
            <SidebarNav onNavigate={() => setMobileOpen(false)} />
          </div>
        </SheetContent>
      </Sheet>
      <div className="lg:hidden">
        <Brand />
      </div>

      <div className="relative ml-1 max-w-md flex-1">
        <Search className="pointer-events-none absolute top-1/2 left-2 size-3.5 -translate-y-1/2 text-muted-foreground" aria-hidden />
        <Input
          type="search"
          role="searchbox"
          aria-label="Search resources on this page"
          placeholder="Search this list…"
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          className="h-8 pl-7 text-sm"
        />
      </div>

      <div className="ml-auto flex items-center gap-1">
        <SimulatedBadge />
        <LiveIndicator />
        {user && (
          <span className="hidden items-center gap-0.5 rounded-md px-2 text-xs text-muted-foreground xl:inline-flex" aria-label="Account ID">
            Account <span className="font-mono text-foreground">{user.account_id}</span>
            <CopyButton value={user.account_id} label="Copy account ID" />
          </span>
        )}
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="sm" aria-label="User menu" className="gap-1.5">
              <UserIcon className="size-4" aria-hidden />
              <span className="hidden max-w-32 truncate text-xs sm:inline">{user?.username}</span>
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="w-60">
            <DropdownMenuLabel className="grid gap-0.5">
              <span className="text-sm">{user?.username}</span>
              <span className="text-xs font-normal text-muted-foreground">
                Role: {user?.role} · Account <span className="font-mono">{user?.account_id}</span>
              </span>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">Theme</DropdownMenuLabel>
            <DropdownMenuRadioGroup value={theme ?? "system"} onValueChange={setTheme}>
              <DropdownMenuRadioItem value="light">
                <Sun /> Light
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="dark">
                <Moon /> Dark
              </DropdownMenuRadioItem>
              <DropdownMenuRadioItem value="system">
                <Monitor /> System
              </DropdownMenuRadioItem>
            </DropdownMenuRadioGroup>
            <DropdownMenuSeparator />
            <DropdownMenuItem asChild>
              <Link href="/settings">
                <Settings /> Settings
              </Link>
            </DropdownMenuItem>
            <DropdownMenuItem onSelect={() => void logout()}>
              <LogOut /> Sign out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </header>
  );
}
