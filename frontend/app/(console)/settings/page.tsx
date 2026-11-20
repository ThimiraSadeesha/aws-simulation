"use client";

import { Monitor, Moon, Sun } from "lucide-react";
import { useTheme } from "next-themes";
import { useSyncExternalStore } from "react";
import { useQuery } from "@tanstack/react-query";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { DetailGrid, Section } from "@/components/console/detail-grid";
import { Mono } from "@/components/console/copy-button";
import { PageHeader } from "@/components/console/page-header";
import { StatusBadge } from "@/components/console/status-badge";
import { useAuth } from "@/features/auth/auth-context";
import { AdminPanel } from "@/features/admin/admin-panel";
import { api } from "@/lib/api/client";
import { formatDate } from "@/lib/format";
import { cn } from "@/lib/utils";

const THEMES = [
  { value: "light", label: "Light", icon: Sun },
  { value: "dark", label: "Dark", icon: Moon },
  { value: "system", label: "System", icon: Monitor },
] as const;

const subscribe = () => () => {};

function ThemePicker() {
  const { theme, setTheme } = useTheme();
  const mounted = useSyncExternalStore(subscribe, () => true, () => false);
  return (
    <div role="radiogroup" aria-label="Theme" className="flex flex-wrap gap-2">
      {THEMES.map((t) => {
        const active = mounted && (theme ?? "system") === t.value;
        const Icon = t.icon;
        return (
          <button
            key={t.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => setTheme(t.value)}
            className={cn(
              "flex w-28 flex-col items-center gap-1 rounded-lg border p-3 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/60",
              active ? "border-sky-600 bg-sky-500/10" : "hover:bg-muted",
            )}
          >
            <Icon className="size-5" aria-hidden />
            {t.label}
          </button>
        );
      })}
    </div>
  );
}

export default function SettingsPage() {
  const { user } = useAuth();
  const identity = useQuery({
    queryKey: ["sts", "caller-identity"],
    queryFn: ({ signal }) => api<{ account: string; arn: string; user_id: string }>("/sts/caller-identity", { signal }),
  });
  const isAdmin = user?.role === "admin";

  return (
    <div>
      <PageHeader title="Settings" description="Your profile, appearance and — for administrators — platform administration." />
      <Tabs defaultValue="profile">
        <TabsList>
          <TabsTrigger value="profile">Profile</TabsTrigger>
          <TabsTrigger value="appearance">Appearance</TabsTrigger>
          {isAdmin && <TabsTrigger value="admin">Administration</TabsTrigger>}
        </TabsList>
        <TabsContent value="profile" className="mt-4 grid gap-4">
          <Section title="Profile">
            <DetailGrid
              items={[
                { label: "Username", value: user?.username },
                { label: "Email", value: user?.email || "—" },
                { label: "Platform role", value: user ? <StatusBadge status={user.role === "admin" ? "active" : "neutral"} label={user.role} /> : null },
                { label: "Account ID", value: <Mono copy>{user?.account_id}</Mono> },
                { label: "Member since", value: formatDate(user?.created_at) },
                { label: "User ID", value: user?.id },
              ]}
            />
          </Section>
          <Section title="Caller identity (sts:GetCallerIdentity)">
            {identity.isError ? (
              <p className="text-sm text-muted-foreground">Unavailable: {(identity.error as Error).message}</p>
            ) : (
              <DetailGrid
                items={[
                  { label: "Account", value: <Mono>{identity.data?.account}</Mono> },
                  { label: "ARN", value: <Mono copy>{identity.data?.arn}</Mono> },
                  { label: "User ID", value: <Mono>{identity.data?.user_id}</Mono> },
                ]}
              />
            )}
            <p className="mt-3 text-xs text-muted-foreground">
              The console session acts as the account root user. Create IAM users with access keys for least-privilege CLI access.
            </p>
          </Section>
        </TabsContent>
        <TabsContent value="appearance" className="mt-4">
          <Section title="Theme">
            <ThemePicker />
          </Section>
        </TabsContent>
        {isAdmin && (
          <TabsContent value="admin" className="mt-4">
            <AdminPanel />
          </TabsContent>
        )}
      </Tabs>
    </div>
  );
}
