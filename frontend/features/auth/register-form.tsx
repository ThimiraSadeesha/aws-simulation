"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useState } from "react";
import { Loader2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { FormError, FormField } from "@/components/console/form-field";
import { errorMessage } from "@/lib/api/client";
import { hasErrors, validateRegister, type FieldErrors } from "@/lib/validation";
import { useAuth } from "./auth-context";

export function RegisterForm() {
  const { register, login } = useAuth();
  const router = useRouter();
  const [form, setForm] = useState({ username: "", email: "", password: "", confirm: "" });
  const [errors, setErrors] = useState<FieldErrors>({});
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);

  const set = (k: keyof typeof form) => (e: React.ChangeEvent<HTMLInputElement>) => setForm((f) => ({ ...f, [k]: e.target.value }));

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    const v = validateRegister(form);
    setErrors(v);
    if (hasErrors(v)) return;
    setPending(true);
    try {
      await register(form.username, form.password, form.email || undefined);
      try {
        await login(form.username, form.password);
        router.replace("/dashboard");
      } catch {
        router.replace(`/login?registered=1&username=${encodeURIComponent(form.username)}`);
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setPending(false);
    }
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Create account</CardTitle>
        <CardDescription>Each CloudSim account gets its own simulated 12-digit account ID. The first user becomes the platform admin.</CardDescription>
      </CardHeader>
      <form onSubmit={submit} noValidate>
        <CardContent className="grid gap-3">
          <FormError message={error} />
          <FormField label="Username" required error={errors.username} hint="3–32 characters: letters, digits, '_', '.', '-'.">
            {(p) => <Input {...p} name="username" autoComplete="username" value={form.username} onChange={set("username")} autoFocus />}
          </FormField>
          <FormField label="Email (optional)" error={errors.email}>
            {(p) => <Input {...p} name="email" type="email" autoComplete="email" value={form.email} onChange={set("email")} />}
          </FormField>
          <FormField label="Password" required error={errors.password} hint="At least 8 characters.">
            {(p) => <Input {...p} name="password" type="password" autoComplete="new-password" value={form.password} onChange={set("password")} />}
          </FormField>
          <FormField label="Confirm password" required error={errors.confirm}>
            {(p) => <Input {...p} name="confirm" type="password" autoComplete="new-password" value={form.confirm} onChange={set("confirm")} />}
          </FormField>
        </CardContent>
        <CardFooter className="mt-4 flex flex-col items-stretch gap-3">
          <Button type="submit" disabled={pending}>
            {pending && <Loader2 className="animate-spin" />} Create account
          </Button>
          <p className="text-center text-sm text-muted-foreground">
            Already have an account?{" "}
            <Link href="/login" className="font-medium text-foreground underline-offset-4 hover:underline">
              Sign in
            </Link>
          </p>
        </CardFooter>
      </form>
    </Card>
  );
}
