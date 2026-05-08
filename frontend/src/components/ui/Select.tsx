"use client";

/**
 * Compatibility-layered Select.
 *
 * Two APIs live in this file:
 *
 * 1. shadcn/ui primitives (preferred for new code) — exported as
 *    SelectRoot, SelectTrigger, SelectContent, SelectItem, SelectValue, …
 *
 * 2. Legacy `<Select options value onChange />` API — the default
 *    `Select` export, used by existing pages. It composes primitives 1.
 */

import * as React from "react";
import { Select as SelectPrimitive } from "radix-ui";
import { CheckIcon, ChevronDownIcon, ChevronUpIcon } from "lucide-react";

import { cn } from "@/lib/utils";

// ─── shadcn primitives ─────────────────────────────────────────────────────

function SelectRoot(props: React.ComponentProps<typeof SelectPrimitive.Root>) {
  return <SelectPrimitive.Root data-slot="select" {...props} />;
}

function SelectGroup(
  props: React.ComponentProps<typeof SelectPrimitive.Group>,
) {
  return <SelectPrimitive.Group data-slot="select-group" {...props} />;
}

function SelectValue(
  props: React.ComponentProps<typeof SelectPrimitive.Value>,
) {
  return <SelectPrimitive.Value data-slot="select-value" {...props} />;
}

function SelectTrigger({
  className,
  size = "default",
  children,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Trigger> & {
  size?: "sm" | "default";
}) {
  return (
    <SelectPrimitive.Trigger
      data-slot="select-trigger"
      data-size={size}
      className={cn(
        "flex w-fit items-center justify-between gap-1.5 rounded-md border border-input bg-transparent py-2 pr-2 pl-3 text-sm whitespace-nowrap transition-colors outline-none focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/50 disabled:cursor-not-allowed disabled:opacity-50 data-placeholder:text-muted-foreground data-[size=default]:h-9 data-[size=sm]:h-8",
        className,
      )}
      {...props}
    >
      {children}
      <SelectPrimitive.Icon asChild>
        <ChevronDownIcon className="size-4 text-muted-foreground" />
      </SelectPrimitive.Icon>
    </SelectPrimitive.Trigger>
  );
}

function SelectContent({
  className,
  children,
  position = "popper",
  align = "start",
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Content>) {
  return (
    <SelectPrimitive.Portal>
      <SelectPrimitive.Content
        data-slot="select-content"
        position={position}
        align={align}
        className={cn(
          "z-50 max-h-[var(--radix-select-content-available-height)] min-w-[10rem] overflow-y-auto rounded-md border bg-popover p-1 text-popover-foreground shadow-md",
          className,
        )}
        {...props}
      >
        <SelectPrimitive.ScrollUpButton className="flex justify-center py-1">
          <ChevronUpIcon className="size-4" />
        </SelectPrimitive.ScrollUpButton>
        <SelectPrimitive.Viewport>{children}</SelectPrimitive.Viewport>
        <SelectPrimitive.ScrollDownButton className="flex justify-center py-1">
          <ChevronDownIcon className="size-4" />
        </SelectPrimitive.ScrollDownButton>
      </SelectPrimitive.Content>
    </SelectPrimitive.Portal>
  );
}

function SelectLabel({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Label>) {
  return (
    <SelectPrimitive.Label
      data-slot="select-label"
      className={cn("px-2 py-1 text-xs text-muted-foreground", className)}
      {...props}
    />
  );
}

function SelectItem({
  className,
  children,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Item>) {
  return (
    <SelectPrimitive.Item
      data-slot="select-item"
      className={cn(
        "relative flex cursor-default items-center gap-2 rounded-sm py-1.5 pr-8 pl-2 text-sm outline-none select-none focus:bg-accent focus:text-accent-foreground data-[disabled]:pointer-events-none data-[disabled]:opacity-50",
        className,
      )}
      {...props}
    >
      <span className="absolute right-2 flex size-3.5 items-center justify-center">
        <SelectPrimitive.ItemIndicator>
          <CheckIcon className="size-4" />
        </SelectPrimitive.ItemIndicator>
      </span>
      <SelectPrimitive.ItemText>{children}</SelectPrimitive.ItemText>
    </SelectPrimitive.Item>
  );
}

function SelectSeparator({
  className,
  ...props
}: React.ComponentProps<typeof SelectPrimitive.Separator>) {
  return (
    <SelectPrimitive.Separator
      data-slot="select-separator"
      className={cn("-mx-1 my-1 h-px bg-border", className)}
      {...props}
    />
  );
}

// ─── legacy Select wrapper ─────────────────────────────────────────────────

interface LegacyOption {
  value: string;
  label: string;
}

interface LegacySelectProps {
  options: LegacyOption[];
  value?: string;
  onChange: (value: string) => void;
  placeholder?: string;
  className?: string;
  /** Tailwind width class — kept for backward compatibility. */
  width?: string;
  disabled?: boolean;
}

function Select({
  options,
  value,
  onChange,
  placeholder = "Select…",
  className,
  width,
  disabled,
}: LegacySelectProps) {
  return (
    <SelectRoot value={value} onValueChange={onChange} disabled={disabled}>
      <SelectTrigger className={cn(width, className)}>
        <SelectValue placeholder={placeholder} />
      </SelectTrigger>
      <SelectContent>
        {options.map((opt) => (
          <SelectItem key={opt.value} value={opt.value}>
            {opt.label}
          </SelectItem>
        ))}
      </SelectContent>
    </SelectRoot>
  );
}

export {
  Select, // legacy API (default for existing call sites)
  SelectRoot, // shadcn Root (preferred)
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectLabel,
  SelectSeparator,
  SelectTrigger,
  SelectValue,
};
