"use client";

import { Suspense } from "react";
import { useSearchParams } from "next/navigation";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Card } from "@/components/ui/Card";

/**
 * OAuth 2.1 consent page (ADR-0009). The backend /oauth/authorize GET
 * validates the request and redirects here with the OAuth params on the query
 * string; this page collects the user's credentials + Allow/Deny decision and
 * POSTs them straight back to /oauth/authorize.
 *
 * The form is a NATIVE <form method="post"> (not fetch/BFF): on approval the
 * backend responds 302 to the client's redirect_uri — often a custom scheme
 * like claude-desktop:// — which only a real browser navigation can follow.
 * A fetch-based submit would trap that redirect.
 */

// Where the consent form POSTs. Same-origin default works when the ingress
// routes /oauth/* to the IAM plane; override for a split-origin deployment.
const AUTHORIZE_URL =
  process.env.NEXT_PUBLIC_OAUTH_AUTHORIZE_URL || "/oauth/authorize";

function ConsentForm() {
  const search = useSearchParams();

  const clientID = search.get("client_id") || "";
  const redirectURI = search.get("redirect_uri") || "";
  const scope = search.get("scope") || "";
  const state = search.get("state") || "";
  const codeChallenge = search.get("code_challenge") || "";
  const codeChallengeMethod = search.get("code_challenge_method") || "S256";
  const resource = search.get("resource") || "";
  const hadError = search.get("error") !== null;

  const scopes = scope.split(/\s+/).filter(Boolean);

  return (
    <div className="flex min-h-screen items-center justify-center bg-background p-6">
      <Card className="w-full max-w-sm space-y-6 p-8">
        <div className="space-y-1">
          <h1 className="text-2xl font-semibold tracking-tight">
            Authorize access
          </h1>
          <p className="text-sm text-muted-foreground">
            <span className="font-medium text-foreground">{clientID}</span> is
            requesting access to your Paladin account.
          </p>
        </div>

        {scopes.length > 0 ? (
          <div className="space-y-1.5">
            <p className="text-sm font-medium">This will allow it to:</p>
            <ul className="list-disc space-y-1 pl-5 text-sm text-muted-foreground">
              {scopes.map((s) => (
                <li key={s}>{s}</li>
              ))}
            </ul>
          </div>
        ) : null}

        <form className="space-y-4" method="post" action={AUTHORIZE_URL}>
          {/* OAuth request params round-tripped back to the backend. */}
          <input type="hidden" name="client_id" value={clientID} />
          <input type="hidden" name="redirect_uri" value={redirectURI} />
          <input type="hidden" name="scope" value={scope} />
          <input type="hidden" name="state" value={state} />
          <input type="hidden" name="code_challenge" value={codeChallenge} />
          <input
            type="hidden"
            name="code_challenge_method"
            value={codeChallengeMethod}
          />
          <input type="hidden" name="resource" value={resource} />

          <div className="space-y-1.5">
            <Label htmlFor="username">Email or username</Label>
            <Input
              id="username"
              name="username"
              autoComplete="username"
              autoFocus
              required
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="password">Password</Label>
            <Input
              id="password"
              name="password"
              type="password"
              autoComplete="current-password"
              required
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="tenant">Tenant (optional)</Label>
            <Input id="tenant" name="tenant" placeholder="tenant id" />
          </div>

          {hadError ? (
            <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive">
              Invalid credentials or tenant. Please try again.
            </div>
          ) : null}

          <div className="flex gap-2">
            <Button
              type="submit"
              name="action"
              value="allow"
              className="flex-1"
            >
              Allow
            </Button>
            <Button
              type="submit"
              name="action"
              value="deny"
              variant="outline"
              className="flex-1"
            >
              Deny
            </Button>
          </div>
        </form>
      </Card>
    </div>
  );
}

export default function ConsentPage() {
  // useSearchParams requires a Suspense boundary under the Next.js app router.
  return (
    <Suspense>
      <ConsentForm />
    </Suspense>
  );
}
