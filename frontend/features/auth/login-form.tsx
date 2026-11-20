"use client";

import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useEffect, useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { FormError, FormField } from "@/components/console/form-field";
import { errorMessage } from "@/lib/api/client";
import { useAuth } from "./auth-context";

function safeNext(next: string | null): string {
  if (next && next.startsWith("/") && !next.startsWith("//")) return next;
  return "/dashboard";
}

export function LoginForm() {
  const { login, status } = useAuth();
  const router = useRouter();
  const params = useSearchParams();
  const next = safeNext(params.get("next"));
  const [username, setUsername] = useState(params.get("username") ?? "");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const registered = params.get("registered") === "1";

  useEffect(() => {
    if (status === "authenticated") router.replace(next);
  }, [status, router, next]);

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    if (!username || !password) {
      setError("Enter your username and password.");
      return;
    }
    setPending(true);
    try {
      await login(username, password);
      router.replace(next);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setPending(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Sign in</CardTitle>
        <CardDescription>Sign in to your CloudSim account (root user of your simulated account).</CardDescription>
      </CardHeader>
      <form onSubmit={submit} noValidate>
        <CardContent className="grid gap-3">
          {registered && (
            <div role="status" className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-800 dark:text-emerald-300">
              Account created. Sign in to continue.
            </div>
          )}
          <FormError message={error} />
          <FormField label="Username" required>
            {(p) => <Input {...p} name="username" autoComplete="username" value={username} onChange={(e) => setUsername(e.target.value)} autoFocus />}
          </FormField>
          <FormField label="Password" required>
            {(p) => (
              <Input {...p} name="password" type="password" autoComplete="current-password" value={password} onChange={(e) => setPassword(e.target.value)} />
            )}
          </FormField>
        </CardContent>
        <CardFooter className="mt-4 flex flex-col items-stretch gap-3">
          <Button type="submit" disabled={pending}>
            {pending && <Loader2 className="animate-spin" />} Sign in
          </Button>
          <p className="text-center text-sm text-muted-foreground">
            New to CloudSim?{" "}
            <Link href="/register" className="font-medium text-foreground underline-offset-4 hover:underline">
              Create an account
            </Link>
          </p>
        </CardFooter>
      </form>
    </Card>
  );
}
