"use client";

import { useEffect, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card } from "@/components/ui/Card";
import { useAuth } from "@/context/AuthContext";

/**
 * Login page — minimal local-IdP form. Talks to /api/auth/login through
 * AuthContext, which seeds the in-memory tokenStore on success and the BFF
 * sets refresh-token cookies. On success, bounce to ?next=… (default /).
 *
 * Federated/upstream login (OIDC code flow) reuses the same form: the
 * upstream redirect drops the code into ?upstream_code= and we POST it.
 */

export default function LoginPage() {
  const router = useRouter();
  const search = useSearchParams();
  const next = search.get("next") || "/";

  const { status, error, login } = useAuth();
  const [subject, setSubject] = useState("");
  const [password, setPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [localError, setLocalError] = useState<string | null>(null);

  // If we're already authenticated (rehydrated from /me), bounce out.
  useEffect(() => {
    if (status === "authenticated") router.replace(next);
  }, [status, next, router]);

  // Auto-submit upstream OIDC code if present in the URL.
  useEffect(() => {
    const code = search.get("upstream_code");
    if (!code) return;
    let cancelled = false;
    (async () => {
      setSubmitting(true);
      try {
        await login("", "", code);
        if (!cancelled) router.replace(next);
      } catch (e) {
        if (!cancelled) setLocalError((e as Error).message);
      } finally {
        if (!cancelled) setSubmitting(false);
      }
    })();
    return () => {
      cancelled = true;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    setLocalError(null);
    setSubmitting(true);
    try {
      await login(subject, password);
      router.replace(next);
    } catch (err) {
      setLocalError((err as Error).message);
    } finally {
      setSubmitting(false);
    }
  };

  const displayError = localError || error;

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <Card className="w-full max-w-sm space-y-6 p-8">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">Paladin</h1>
          <p className="text-sm text-muted-foreground">Sign in to continue</p>
        </div>

        <form className="space-y-4" onSubmit={handleSubmit}>
          <div className="space-y-1.5">
            <Label htmlFor="subject">Email or username</Label>
            <Input
              id="subject"
              autoComplete="username"
              autoFocus
              required
              value={subject}
              onChange={(e) => setSubject(e.target.value)}
              disabled={submitting}
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              disabled={submitting}
            />
          </div>

          {displayError ? (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              {displayError}
            </div>
          ) : null}

          <Button type="submit" className="w-full" disabled={submitting}>
            {submitting ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </Card>
    </div>
  );
}
