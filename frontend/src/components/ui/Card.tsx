import * as React from "react";

import { IconChip, type IconTone } from "@/components/ui/IconChip";
import { cn } from "@/lib/utils";

/**
 * Card variants accepted both by shadcn (`size`) and by the legacy
 * Card component (`variant`). The `variant` prop is consumed locally —
 * it never reaches the DOM and avoids React unknown-attribute warnings.
 */
/**
 * overflow-clip, not -hidden: both cut the content to the rounded corners,
 * but -hidden makes the card a scroll container, and a sticky header inside
 * one sticks to the card, which never scrolls, instead of to the page.
 */
/**
 * A card is a plane above the page: an edge that shows against the ground
 * and a raised shadow. The flat variants below lie on the page instead.
 */
export const CARD_PLANE = "ring-1 ring-border shadow-raised";

/**
 * The header that opens a card sits on a band of its own, ruled off from the
 * body. The card gives up its top padding for it (CARD_OPENS_WITH_HEADER), so
 * the band reaches the edge whatever padding the card was given; pulling the
 * band up by the default padding instead pushed it past the edge of a card
 * that had none, clipping its icon.
 */
export const CARD_HEADER_BAND =
  "first:border-b first:border-border first:bg-band first:py-3 group-data-[size=sm]/card:first:py-2";

/**
 * A card that opens with its header has no top padding above the band, a
 * header first inside a form that fills the card included.
 */
export const CARD_OPENS_WITH_HEADER =
  "has-[>[data-slot=card-header]:first-child]:pt-0 has-[>form:first-child>[data-slot=card-header]:first-child]:pt-0";

function Card({
  className,
  size = "default",
  variant,
  ...props
}: React.ComponentProps<"div"> & {
  size?: "default" | "sm";
  /** Legacy: "default" | "subtle" | "outline" | "elevated". Maps to ring/shadow. */
  variant?: "default" | "subtle" | "outline" | "elevated" | "glass";
}) {
  const variantClass: Record<NonNullable<typeof variant>, string> = {
    default: CARD_PLANE,
    subtle: "bg-muted/30 ring-1 ring-foreground/5",
    outline: "bg-transparent ring-1 ring-border",
    elevated: CARD_PLANE,
    glass: "bg-card/60 backdrop-blur-md ring-1 ring-foreground/10",
  };
  return (
    <div
      data-slot="card"
      data-size={size}
      className={cn(
        "group/card flex flex-col gap-4 overflow-clip rounded-xl bg-card py-4 text-sm text-card-foreground has-data-[slot=card-footer]:pb-0 has-[>img:first-child]:pt-0 data-[size=sm]:gap-3 data-[size=sm]:py-3 data-[size=sm]:has-data-[slot=card-footer]:pb-0 *:[img:first-child]:rounded-t-xl *:[img:last-child]:rounded-b-xl",
        // The flat variants replace the plane rather than undo it: the
        // class merge does not know shadow-raised for a shadow, so a
        // shadow-none beside it would not win.
        variantClass[variant ?? "default"],
        CARD_OPENS_WITH_HEADER,
        className,
      )}
      {...props}
    />
  );
}

function CardHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-header"
      className={cn(
        "group/card-header @container/card-header grid auto-rows-min items-start gap-1 rounded-t-xl px-4 group-data-[size=sm]/card:px-3 has-data-[slot=card-action]:grid-cols-[1fr_auto] has-data-[slot=card-description]:grid-rows-[auto_auto] [.border-b]:pb-4 group-data-[size=sm]/card:[.border-b]:pb-3",
        CARD_HEADER_BAND,
        className,
      )}
      {...props}
    />
  );
}

/**
 * A card title is a section heading, so it renders as one.
 *
 * It used to be a `<div>`, which meant every section title in the console —
 * Password, Preferences, Identity, Quotas — was invisible to heading
 * navigation, the primary way a screen-reader user moves through a page. A
 * sighted user saw structure the markup did not carry.
 *
 * `level` defaults to 2: the shell spends h1 on the page title (PageHeader),
 * and every card in the console today sits directly under it rather than
 * inside a subsection, so h2 is the level that neither outranks the page nor
 * skips a rank. A card nested inside a section that has its own heading
 * should pass the next level down. Keep the children inline — a heading may
 * not contain flow content.
 */
function CardTitle({
  className,
  level = 2,
  icon,
  tone,
  children,
  ...props
}: React.ComponentProps<"h2"> & {
  level?: 1 | 2 | 3 | 4 | 5 | 6;
  /** The section's icon, drawn on a tinted chip before the title. */
  icon?: React.ElementType;
  /** The chip's tone; the accent unless the card says otherwise. */
  tone?: IconTone;
}) {
  const Heading = `h${level}` as const;
  return (
    <Heading
      data-slot="card-title"
      className={cn(
        "font-heading text-base leading-snug font-medium group-data-[size=sm]/card:text-sm",
        icon && "flex items-center gap-2",
        className,
      )}
      {...props}
    >
      {icon && <IconChip icon={icon} tone={tone} />}
      {children}
    </Heading>
  );
}

function CardDescription({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-description"
      className={cn("text-sm text-muted-foreground", className)}
      {...props}
    />
  );
}

function CardAction({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-action"
      className={cn(
        "col-start-2 row-span-2 row-start-1 self-start justify-self-end",
        className,
      )}
      {...props}
    />
  );
}

function CardContent({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-content"
      className={cn("px-4 group-data-[size=sm]/card:px-3", className)}
      {...props}
    />
  );
}

function CardFooter({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="card-footer"
      className={cn(
        "flex items-center rounded-b-xl border-t bg-muted/50 p-4 group-data-[size=sm]/card:p-3",
        className,
      )}
      {...props}
    />
  );
}

export {
  Card,
  CardHeader,
  CardFooter,
  CardTitle,
  CardAction,
  CardDescription,
  CardContent,
};
