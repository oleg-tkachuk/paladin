"use client";

import { useState } from "react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useNotification } from "@/components/ui/Notification";
import { capabilityClient } from "@/lib/connect/client";
import { errorMessage } from "@/hooks/errorContract";
import { formatMoney, moneyIsPositive } from "@/lib/format/money";
import type {
  CapabilityBiscuitCopyUsage,
  CapabilityServiceGetBiscuitUsageResponse,
} from "@/gen/paladin/admin/v1/capability_service_pb";
import type { Money } from "@/gen/google/type/money_pb";
import { isJWT, shortRevocationId } from "./_biscuit";
import { SPEND_DIGITS } from "./_constants";

// rowUnit is the unit of one copy's amounts. Its spent, budget and hold
// share it, so an amount that arrives without one borrows the row's.
function rowUnit(c: CapabilityBiscuitCopyUsage): string | undefined {
  return (
    c.maxBudget?.currencyCode ||
    c.spent?.currencyCode ||
    c.reserved?.currencyCode ||
    undefined
  );
}

/**
 * Shows one copy of a capability's Biscuit: each request and budget limit in
 * force on it — its own and those of the copies it was narrowed from — with
 * what has been used against each. Copies are listed nowhere, so the
 * operator pastes the copy itself.
 */
export function BiscuitCopyUsageDialog({
  open,
  onClose,
}: {
  open: boolean;
  onClose: () => void;
}) {
  const { showNotification } = useNotification();
  const [token, setToken] = useState("");
  const [loading, setLoading] = useState(false);
  const [result, setResult] =
    useState<CapabilityServiceGetBiscuitUsageResponse | null>(null);

  const trimmed = token.trim();
  const jwt = isJWT(trimmed);

  const handleClose = () => {
    setToken("");
    setResult(null);
    onClose();
  };

  const handleCheck = async () => {
    if (!trimmed || jwt) return;
    setLoading(true);
    try {
      setResult(await capabilityClient.getBiscuitUsage({ token: trimmed }));
    } catch (err) {
      setResult(null);
      showNotification({
        type: "error",
        title: "Usage lookup failed",
        message: errorMessage(err, "Usage lookup failed"),
      });
    } finally {
      setLoading(false);
    }
  };

  const money = (m: Money | undefined, fallbackUnit: string | undefined) =>
    formatMoney(m, { fallbackUnit, fractionDigits: SPEND_DIGITS });

  return (
    <Dialog open={open} onOpenChange={(o) => !o && handleClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Usage of one Biscuit copy</DialogTitle>
          <DialogDescription>
            Paste the Biscuit an agent holds to see the limits narrowed onto it
            and what has been used against each. Every limit also counts the
            copies narrowed from it.
          </DialogDescription>
        </DialogHeader>
        <div className="space-y-1.5">
          <Label className="text-xs" htmlFor="copy-usage-token">
            Biscuit
          </Label>
          <Textarea
            id="copy-usage-token"
            rows={4}
            spellCheck={false}
            autoComplete="off"
            className="font-mono text-xs"
            value={token}
            onChange={(e) => {
              setToken(e.target.value);
              setResult(null);
            }}
            aria-invalid={jwt}
            aria-describedby={jwt ? "copy-usage-jwt" : undefined}
          />
          {jwt && (
            <p id="copy-usage-jwt" className="text-xs text-destructive">
              This is a JWT, not a Biscuit. A JWT has no copies: its usage is
              the capability&apos;s, in the table.
            </p>
          )}
        </div>

        {result && (
          <div className="space-y-2" data-testid="copy-usage-result">
            <p className="text-xs text-muted-foreground">
              A copy of capability{" "}
              <span className="font-mono text-foreground">
                {result.capabilityId}
              </span>
            </p>
            {result.copies.length === 0 ? (
              <p className="text-sm">
                No limits were narrowed onto this copy: it uses the
                capability&apos;s own, shown in the table.
              </p>
            ) : (
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Block</TableHead>
                    <TableHead>Requests</TableHead>
                    <TableHead>Spent</TableHead>
                    <TableHead>Held</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {result.copies.map((c, i) => (
                    <TableRow key={shortRevocationId(c.revocationId) + i}>
                      <TableCell className="font-mono text-xs">
                        {shortRevocationId(c.revocationId)}
                        {i === 0 && (
                          <span className="ml-1 text-muted-foreground">
                            (innermost)
                          </span>
                        )}
                      </TableCell>
                      <TableCell>
                        {c.requestCount.toString()}
                        {c.maxRequests > 0n && ` / ${c.maxRequests}`}
                      </TableCell>
                      <TableCell>
                        {money(c.spent, rowUnit(c))}
                        {moneyIsPositive(c.maxBudget) &&
                          ` / ${money(c.maxBudget, rowUnit(c))}`}
                      </TableCell>
                      <TableCell>{money(c.reserved, rowUnit(c))}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            )}
          </div>
        )}

        <DialogFooter>
          <Button variant="outline" onClick={handleClose}>
            Close
          </Button>
          <Button
            onClick={() => void handleCheck()}
            disabled={!trimmed || jwt || loading}
          >
            Check usage
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
