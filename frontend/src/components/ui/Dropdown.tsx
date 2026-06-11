"use client";

import React, {
  useState,
  useRef,
  useEffect,
  createContext,
  useContext,
} from "react";
import { createPortal } from "react-dom";
import { cn } from "@/lib/utils";

// --- Context ---

interface DropdownContextType {
  open: boolean;
  setOpen: (open: boolean) => void;
  triggerRef: React.RefObject<HTMLDivElement | null>;
  menuRef: React.RefObject<HTMLDivElement | null>;
  align: "left" | "right";
  width: string;
  coords: { top: number; left: number; triggerWidth: number };
}

const DropdownContext = createContext<DropdownContextType | undefined>(
  undefined,
);

function useDropdownContext() {
  const context = useContext(DropdownContext);
  if (!context) {
    throw new Error(
      "Dropdown sub-components must be used within a <Dropdown />",
    );
  }
  return context;
}

// --- Components ---

interface DropdownProps {
  children: React.ReactNode;
  align?: "left" | "right";
  width?: string;
  className?: string;
}

/**
 * Root Dropdown component that provides state and refs via context.
 */
export const Dropdown = ({
  children,
  align = "left",
  width = "w-64",
  className,
}: DropdownProps) => {
  const [open, setOpen] = useState(false);
  const [coords, setCoords] = useState({ top: 0, left: 0, triggerWidth: 0 });
  const triggerRef = useRef<HTMLDivElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const handleClickOutside = (event: MouseEvent) => {
      if (
        triggerRef.current &&
        !triggerRef.current.contains(event.target as Node) &&
        menuRef.current &&
        !menuRef.current.contains(event.target as Node)
      ) {
        setOpen(false);
      }
    };

    if (open) {
      document.addEventListener("mousedown", handleClickOutside);

      const updateCoords = () => {
        if (triggerRef.current) {
          const rect = triggerRef.current.getBoundingClientRect();
          setCoords({
            top: rect.bottom + window.scrollY + 8,
            left:
              align === "left"
                ? rect.left + window.scrollX
                : rect.right + window.scrollX,
            triggerWidth: rect.width,
          });
        }
      };

      updateCoords();
      // passive: updateCoords only reads layout — never preventDefault —
      // so the browser shouldn't block scrolling on this listener.
      window.addEventListener("scroll", updateCoords, {
        capture: true,
        passive: true,
      });
      window.addEventListener("resize", updateCoords, { passive: true });

      return () => {
        document.removeEventListener("mousedown", handleClickOutside);
        window.removeEventListener("scroll", updateCoords, { capture: true });
        window.removeEventListener("resize", updateCoords);
      };
    }
  }, [open, align]);

  return (
    <DropdownContext.Provider
      value={{ open, setOpen, triggerRef, menuRef, align, width, coords }}
    >
      <div
        className={cn(
          "relative",
          width === "w-full" ? "block w-full" : "inline-block",
          className,
        )}
        ref={triggerRef}
      >
        {children}
      </div>
    </DropdownContext.Provider>
  );
};

// --- Sub-components ---

interface DropdownTriggerProps {
  children: React.ReactNode | ((props: { open: boolean }) => React.ReactNode);
  className?: string;
  activeClassName?: string;
  disabled?: boolean;
}

Dropdown.Trigger = function DropdownTrigger({
  children,
  className,
  activeClassName,
  disabled,
}: DropdownTriggerProps) {
  const { open, setOpen } = useDropdownContext();

  return (
    <div
      onClick={() => !disabled && setOpen(!open)}
      className={cn(
        "cursor-pointer",
        className,
        open && activeClassName,
        disabled && "opacity-50 cursor-not-allowed pointer-events-none",
      )}
    >
      {typeof children === "function" ? children({ open }) : children}
    </div>
  );
};

interface DropdownMenuProps {
  children:
    | React.ReactNode
    | ((props: { close: () => void }) => React.ReactNode);
  className?: string;
}

Dropdown.Menu = function DropdownMenu({
  children,
  className,
}: DropdownMenuProps) {
  const { open, setOpen, menuRef, coords, align, width } = useDropdownContext();

  if (!open || typeof document === "undefined") return null;

  // When width is "w-full", use the trigger's actual pixel width instead of a CSS class
  // since the portal renders on document.body where "w-full" = viewport width
  const usePixelWidth = width === "w-full";

  return createPortal(
    <div
      ref={menuRef}
      style={{
        position: "absolute",
        top: `${coords.top}px`,
        left: `${coords.left}px`,
        transform: align === "right" ? "translateX(-100%)" : "none",
        ...(usePixelWidth && coords.triggerWidth > 0
          ? { width: `${coords.triggerWidth}px` }
          : {}),
      }}
      className={cn(
        "z-[9999] rounded-2xl bg-popover text-popover-foreground border border-border shadow-[0_20px_50px_rgba(0,0,0,0.5)] p-1.5 animate-fade-in backdrop-blur-2xl ring-1 ring-border/50",
        !usePixelWidth && width,
        className,
      )}
    >
      {typeof children === "function"
        ? children({ close: () => setOpen(false) })
        : children}
    </div>,
    document.body,
  );
};

interface DropdownItemProps {
  children: React.ReactNode;
  onClick?: () => void;
  className?: string;
  closeOnClick?: boolean;
}

Dropdown.Item = function DropdownItem({
  children,
  onClick,
  className,
  closeOnClick = true,
}: DropdownItemProps) {
  const { setOpen } = useDropdownContext();

  return (
    <div
      onClick={() => {
        onClick?.();
        if (closeOnClick) setOpen(false);
      }}
      className={cn(
        "w-full text-left px-3 py-2 text-xs font-bold transition-colors flex items-center justify-between cursor-pointer",
        "text-muted-foreground hover:text-foreground hover:bg-accent rounded-xl",
        className,
      )}
    >
      {children}
    </div>
  );
};
