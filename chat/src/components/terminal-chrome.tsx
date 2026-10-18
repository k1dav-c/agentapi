"use client";

import {useEffect, useState, type RefObject} from "react";
import {Keyboard, KeyboardOff} from "lucide-react";
import type {Terminal} from "@xterm/xterm";
import {IOS_FORM_BAR_HEIGHT, isIOS, keyboardInset, terminalShortcuts, type StickyModifier, type TerminalShortcut} from "@/lib/terminal-keys";
import {useMediaQuery} from "@/lib/use-media-query";

// The look both terminals share.
export const TERMINAL_FONT_SIZE = 13;
export const TERMINAL_LINE_HEIGHT = 1.2;
export const TERMINAL_OPTIONS = {
  fontSize: TERMINAL_FONT_SIZE,
  lineHeight: TERMINAL_LINE_HEIGHT,
  fontFamily: 'Menlo, "DejaVu Sans Mono", Consolas, "Courier New", monospace',
  theme: {background: "#0b0d10", foreground: "#e6e6e6"},
};

// What the terminals (TTY mode, the native terminal) share about typing on
// a phone: whether the on-screen keyboard is up, and how much of the page
// it covers (iOS doesn't shrink the page for it), so the key bar can sit
// above it.
export function useTerminalKeyboard() {
  // Touch screens type through an on-screen keyboard, which only opens
  // while the terminal's (hidden) input has focus.
  const touch = useMediaQuery("(pointer: coarse)");
  const [keyboardOpen, setKeyboardOpen] = useState(false);
  const [inset, setInset] = useState(0);
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const formBar = isIOS(navigator.userAgent, navigator.platform, navigator.maxTouchPoints) ? IOS_FORM_BAR_HEIGHT : 0;
    const update = () => setInset(keyboardInset(window.innerHeight, viewport, formBar));
    update();
    viewport.addEventListener("resize", update);
    viewport.addEventListener("scroll", update);
    return () => {
      viewport.removeEventListener("resize", update);
      viewport.removeEventListener("scroll", update);
    };
  }, []);
  return {touch, keyboardOpen, setKeyboardOpen, inset};
}

// Sets up the input xterm types through: tells phone keyboards it is a
// terminal (no autocorrect or capitals) and reports whether it has focus,
// which is whether the on-screen keyboard is up.
export function prepareTerminalInput(container: HTMLElement, onFocusChange: (focused: boolean) => void) {
  const textarea = container.querySelector<HTMLTextAreaElement>(".xterm-helper-textarea");
  if (!textarea) return;
  textarea.setAttribute("autocapitalize", "off");
  textarea.setAttribute("autocorrect", "off");
  textarea.setAttribute("spellcheck", "false");
  textarea.setAttribute("enterkeyhint", "enter");
  textarea.addEventListener("focus", () => onFocusChange(true));
  textarea.addEventListener("blur", () => onFocusChange(false));
}

// Keys a phone keyboard can't send, under the terminal. Buttons don't take
// focus, so the terminal (and the on-screen keyboard) stays active. A risky
// key (Ctrl+D, Ctrl+Z) needs a second press.
export function TerminalKeyBar({
  termRef,
  touch,
  keyboardOpen,
  onKeys,
  modifier,
  onModifier,
}: {
  termRef: RefObject<Terminal | null>;
  touch: boolean;
  keyboardOpen: boolean;
  onKeys: (data: string) => void;
  // Ctrl or Alt held for the next key typed on the keyboard.
  modifier: StickyModifier | null;
  onModifier: (modifier: StickyModifier | null) => void;
}) {
  const [armed, setArmed] = useState<string | null>(null);
  useEffect(() => {
    if (!armed) return;
    const timer = window.setTimeout(() => setArmed(null), 3000);
    return () => window.clearTimeout(timer);
  }, [armed]);
  const press = (shortcut: TerminalShortcut) => {
    if (shortcut.risky && armed !== shortcut.label) {
      setArmed(shortcut.label);
      return;
    }
    setArmed(null);
    onModifier(null);
    onKeys(shortcut.value);
    // On a touch screen, don't bring back a keyboard the user put away.
    if (!touch || keyboardOpen) termRef.current?.focus();
  };
  const toggleKeyboard = () => {
    const term = termRef.current;
    if (!term) return;
    // Focus from the tap itself: iOS opens the keyboard only for focus
    // that happens inside a user gesture.
    if (keyboardOpen) {
      term.blur();
      return;
    }
    // Focus without scrolling the terminal sideways to the cursor.
    const textarea = term.textarea;
    if (textarea) textarea.focus({preventScroll: true});
    else term.focus();
  };
  return (
    <div className="flex shrink-0 items-start gap-1.5 border-t bg-background/95 px-3 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] pt-2">
      {touch && (
        <button
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={toggleKeyboard}
          aria-pressed={keyboardOpen}
          aria-label={keyboardOpen ? "Hide keyboard" : "Show keyboard"}
          title={keyboardOpen ? "Hide keyboard" : "Show keyboard"}
          className={`flex h-8 shrink-0 items-center gap-1.5 rounded-md border px-2.5 text-xs font-medium outline-none transition focus-visible:ring-2 focus-visible:ring-ring ${
            keyboardOpen ? "border-foreground bg-foreground text-background" : "bg-card hover:bg-muted"
          }`}
        >
          {keyboardOpen ? <KeyboardOff className="size-4" /> : <Keyboard className="size-4" />}
          {keyboardOpen ? "Hide" : "Keyboard"}
        </button>
      )}
      {(["ctrl", "alt"] as const).map((held) => (
        <button
          key={held}
          type="button"
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => {
            onModifier(modifier === held ? null : held);
            if (!touch || keyboardOpen) termRef.current?.focus();
          }}
          aria-pressed={modifier === held}
          title={`${held === "ctrl" ? "Ctrl" : "Alt"} for the next key you type`}
          className={`h-8 shrink-0 rounded-md border px-2.5 font-mono text-xs outline-none transition focus-visible:ring-2 focus-visible:ring-ring ${
            modifier === held ? "border-foreground bg-foreground text-background" : "bg-card hover:bg-muted"
          }`}
        >
          {held === "ctrl" ? "Ctrl" : "Alt"}
        </button>
      ))}
      <div className="flex min-w-0 flex-1 gap-1.5 overflow-x-auto" role="group" aria-label="Terminal keys">
        {terminalShortcuts.map((shortcut) => (
          <button
            key={shortcut.label}
            type="button"
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => press(shortcut)}
            title={shortcut.risky ? `Send ${shortcut.label} (press twice)` : `Send ${shortcut.label}`}
            aria-label={armed === shortcut.label ? `Send ${shortcut.label}: press again to confirm` : `Send ${shortcut.label}`}
            className={`h-8 shrink-0 rounded-md border px-2.5 font-mono text-xs outline-none transition focus-visible:ring-2 focus-visible:ring-ring ${
              armed === shortcut.label ? "border-state-fault bg-state-fault text-white" : "bg-card hover:bg-muted"
            }`}
          >
            {armed === shortcut.label ? `${shortcut.display} again` : shortcut.display}
          </button>
        ))}
      </div>
    </div>
  );
}
