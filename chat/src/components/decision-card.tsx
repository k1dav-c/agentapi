"use client";

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ComponentProps,
  type ComponentType,
  type ReactNode,
} from "react";
import { Check } from "lucide-react";
import { ProcessedMessage } from "./processed-message";
import { CopyButton } from "./message-list/message-item";
import { cn } from "@/lib/utils";

// The pieces of the cards docked above the composer while the agent needs
// you: its requests (native transport), its terminal prompts (PTY) and the
// login card.

// An option row on a card, styled by what the option does. It may be
// narrower than its text (min-w-0), so a long path in a label wraps instead
// of widening the card past the screen.
export const optionRowClass =
  "flex min-h-10 w-full min-w-0 items-center gap-3 rounded-lg border px-2.5 py-1.5 text-left max-sm:px-3 text-[13px] outline-none transition focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60";

export const TONE_CLASS = {
  primary: "border-primary bg-primary text-primary-foreground hover:bg-primary/90",
  secondary: "border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/70",
  destructive: "border-destructive/45 text-destructive hover:border-destructive/70 hover:bg-destructive/10",
  neutral: "hover:border-state-needs/60 hover:bg-state-needs/10",
} as const;

const TitleClamped = createContext(false);

// Whether the card's title is cut at two lines, so what it says in full
// (a long command) has to be shown below it.
export function useTitleClamped(): boolean {
  return useContext(TitleClamped);
}

// The card: the kind of question in small capitals (with more about it in
// meta), the title (two lines at most), and aside it e.g. which keys pick an
// option. Screen readers name the card by its kind and title.
//
// The card never takes more than 60% of a phone's screen (70% on wider
// ones): the header and children scroll inside it, while the footer (the
// options and what answers them) stays in view at the bottom.
export function DecisionCardShell({
  icon: Icon,
  label,
  meta,
  title,
  description,
  aside,
  footer,
  children,
  ...props
}: {
  icon?: ComponentType<{ className?: string }>;
  label?: string;
  meta?: ReactNode;
  title: ReactNode;
  description?: ReactNode;
  aside?: ReactNode;
  footer?: ReactNode;
  children?: ReactNode;
} & Omit<ComponentProps<"section">, "title" | "className">) {
  const id = useId();
  const titleRef = useRef<HTMLHeadingElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const [clamped, setClamped] = useState(false);
  // A cut title can be shown in full: a hover title doesn't reach a phone.
  const [titleOpen, setTitleOpen] = useState(false);
  const [scrolls, setScrolls] = useState(false);
  const measure = useCallback(() => {
    const heading = titleRef.current;
    const body = bodyRef.current;
    if (heading) setClamped(heading.scrollHeight > heading.clientHeight + 1);
    if (body) setScrolls(body.scrollHeight > body.clientHeight + 1);
  }, []);
  // After every render (the content may have changed), and when the card
  // is resized.
  useLayoutEffect(measure);
  useEffect(() => {
    const observer = new ResizeObserver(measure);
    for (const element of [titleRef.current, bodyRef.current, ...Array.from(bodyRef.current?.children ?? [])]) {
      if (element) observer.observe(element);
    }
    return () => observer.disconnect();
  }, [measure]);

  return (
    <section
      {...props}
      aria-labelledby={label ? `${id}-label ${id}-title` : `${id}-title`}
      className="decision-card flex max-h-[60dvh] flex-col overflow-hidden rounded-xl border border-state-needs/45 bg-card shadow-lg shadow-black/5 sm:max-h-[70dvh]"
    >
      <TitleClamped.Provider value={clamped}>
        <div ref={bodyRef} className="min-h-0 overflow-y-auto overscroll-contain">
          <header className="px-3.5 pb-1 pt-3 max-sm:px-3 max-sm:pt-2.5">
            {label && (
              <div className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.06em] text-state-needs">
                {Icon && <Icon className="size-3.5" />}
                <span id={`${id}-label`}>{label}</span>
                {meta}
              </div>
            )}
            <div className="flex items-start gap-3">
              <h2
                ref={titleRef}
                id={`${id}-title`}
                title={clamped && typeof title === "string" ? title : undefined}
                className={cn("min-w-0 flex-1 text-sm font-semibold leading-5 [overflow-wrap:anywhere]", !titleOpen && "line-clamp-2")}
              >
                {title}
              </h2>
              {aside}
            </div>
            {(clamped || titleOpen) && (
              <button
                type="button"
                onClick={() => setTitleOpen((open) => !open)}
                aria-expanded={titleOpen}
                aria-controls={`${id}-title`}
                className="mt-0.5 rounded text-[11px] font-medium text-muted-foreground underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring"
              >
                {titleOpen ? "Show less" : "Show all"}
              </button>
            )}
            {description && <p className="mt-0.5 text-[13px] text-muted-foreground">{description}</p>}
          </header>
          {children}
        </div>
        {footer && (
          // Its own limit, so a long list of options can't push the content
          // out; a rule shows where the content scrolls.
          <div className={cn("max-h-[35dvh] shrink-0 overflow-y-auto overscroll-contain", scrolls && "border-t")}>{footer}</div>
        )}
      </TitleClamped.Provider>
    </section>
  );
}

// Text from (or for) a terminal, in the terminal's colors.
export function TerminalBlock({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <pre
      className={cn(
        "max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-md bg-term px-3 py-2 font-mono text-xs leading-[1.55] text-term-foreground [overflow-wrap:anywhere]",
        className,
      )}
    >
      {children}
    </pre>
  );
}

const shellPrompt = (
  <span className="mr-2 text-term-foreground/60" aria-hidden="true">
    $
  </span>
);

// A shell command: wrapped, or on one line with a copy button when it is a
// command for the user to run.
export function CommandBlock({ command, copyable = false }: { command: string; copyable?: boolean }) {
  if (!copyable) {
    return (
      // About 12 lines, then it scrolls.
      <TerminalBlock className="max-h-60">
        {shellPrompt}
        {command}
      </TerminalBlock>
    );
  }
  return (
    <div className="flex items-center overflow-hidden rounded-md bg-term pr-1 text-term-foreground">
      <pre className="min-w-0 flex-1 overflow-x-auto whitespace-pre px-3 py-2 font-mono text-xs leading-[1.55]">
        {shellPrompt}
        {command}
      </pre>
      <CopyButton content={command} label="command" onDark />
    </div>
  );
}

// Markdown from the agent (a plan, help for a login) in a box that scrolls
// once it is taller than max-h-32, or a plan's taller limit. On phones it
// has no limit of its own: the card scrolls instead, rather than a box
// inside a box.
export function MarkdownDetail({
  content,
  tall = false,
  small = false,
  ...props
}: { content: string; tall?: boolean; small?: boolean } & Omit<ComponentProps<"div">, "className" | "children">) {
  return (
    <div
      {...props}
      className={`overflow-auto rounded-md border bg-background px-3 py-2 [&_.prose-transcript]:leading-[1.6] [&_h1]:my-2 [&_h1]:text-[15px] [&_h1]:font-semibold [&_h2]:my-2 [&_h2]:text-[14.5px] [&_h2]:font-semibold [&_h3]:my-1.5 [&_h3]:font-semibold [&_:is(h1,h2,h3):first-child]:mt-0 ${
        small ? "[&_.prose-transcript]:text-[13px]" : "[&_.prose-transcript]:text-[13.5px]"
      } ${tall ? "sm:max-h-[min(22rem,42dvh)]" : "sm:max-h-32"}`}
    >
      <ProcessedMessage messageContent={content} isUser={false} renderMode="markdown" />
    </div>
  );
}

// The card's main action, in the "needs you" color; className sizes it.
export function PrimaryButton({ className, ...props }: ComponentProps<"button">) {
  return (
    <button
      type="button"
      {...props}
      className={cn(
        "rounded-lg bg-state-needs text-[13px] font-semibold text-background outline-none transition hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60",
        className,
      )}
    />
  );
}

// The cards' secondary action (Back, Check again next to a primary one).
export function SecondaryButton({ className, ...props }: ComponentProps<"button">) {
  return (
    <button
      type="button"
      {...props}
      className={cn(
        "rounded-lg border text-[13px] outline-none transition hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60",
        className,
      )}
    />
  );
}

// The box of a multiple-choice option, or the circle of a single choice.
export function ChoiceMark({ checked, round = false }: { checked: boolean; round?: boolean }) {
  return (
    <span
      aria-hidden="true"
      className={`grid size-4 shrink-0 place-items-center border ${round ? "rounded-full" : "rounded"} ${
        checked ? "border-state-needs bg-state-needs text-background" : "border-muted-foreground/50"
      }`}
    >
      {checked && <Check className="size-3" strokeWidth={3} />}
    </span>
  );
}

// What the card is doing, as a status line that is always in the page
// (screen readers only announce changes to a live region that already
// exists; an empty one is visually hidden), and errors as an alert in its
// place.
export function CardStatus({ status, error, className }: { status?: ReactNode; error?: string; className: string }) {
  return (
    <>
      <p role="status" className={status && !error ? `${className} text-muted-foreground` : "sr-only"}>
        {status}
      </p>
      <p role="alert" className={error ? `${className} text-destructive` : "sr-only"}>
        {error}
      </p>
    </>
  );
}
