import type { ReactNode } from "react";

// A key as a keycap: the number that picks an option on a decision card
// (inverted on a primary option; not on a phone, which has no number keys
// to press), or, small, a key of a shortcut hint.
export function Kbd({ children, small = false, inverted = false }: { children: ReactNode; small?: boolean; inverted?: boolean }) {
  if (small) return <kbd className="rounded border border-b-2 px-1 font-mono text-[10px] text-muted-foreground">{children}</kbd>;
  return (
    <kbd
      className={`grid min-w-5 shrink-0 max-sm:hidden place-items-center rounded border border-b-2 px-1 font-mono text-[10.5px] ${
        inverted ? "border-primary-foreground/30 bg-primary-foreground/10 text-primary-foreground" : "bg-background text-muted-foreground"
      }`}
    >
      {children}
    </kbd>
  );
}
