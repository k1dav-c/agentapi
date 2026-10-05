"use client";

import {useEffect, useRef, useState} from "react";
import {ArrowLeft, Keyboard, KeyboardOff, TerminalSquare} from "lucide-react";
import {toast} from "sonner";
import type {Terminal} from "@xterm/xterm";
import {useChat} from "./chat-provider";
import {Button} from "./ui/button";
import {OrderedInput, fitTerminalFontSize, screenToTerminalOutput} from "@/lib/tty";
import {keyboardInset, terminalShortcuts, type TerminalShortcut} from "@/lib/terminal-keys";
import {useMediaQuery} from "@/lib/use-media-query";

// The mirror uses the width of the agent's terminal (80 columns unless
// AgentAPI was started with --term-width), so lines wrap exactly as the
// agent drew them. The font scales to fill the window instead.
const DEFAULT_COLUMNS = 80;
const FONT_SIZE = 13;
const LINE_HEIGHT = 1.2;
// Horizontal padding of the terminal panel (p-2 on both sides).
const PANEL_PADDING = 16;

// TTY mode: an escape hatch for when the chat view looks wrong. It shows
// the agent's emulated terminal screen as-is and sends every keystroke
// straight to the agent, like attaching to the terminal.
export function TtyView({onExit}: {onExit: () => void}) {
  const {storageScope, sendTerminalInput, terminalColumns} = useChat();
  const columns = terminalColumns || DEFAULT_COLUMNS;
  const columnsRef = useRef(columns);
  const containerRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  // Re-fits the terminal to the panel; set once the terminal exists.
  const fitRef = useRef<() => void>(() => {});
  const [connected, setConnected] = useState(false);
  // Width of one terminal cell, for the column ruler.
  const [cellWidth, setCellWidth] = useState(0);
  const inputRef = useRef<OrderedInput | null>(null);
  const termRef = useRef<Terminal | null>(null);
  // Touch screens type through an on-screen keyboard, which only opens
  // while the terminal's (hidden) input has focus.
  const touch = useMediaQuery("(pointer: coarse)");
  const [keyboardOpen, setKeyboardOpen] = useState(false);
  // How much of the page the on-screen keyboard covers (iOS doesn't shrink
  // the page for it), so the key bar can sit above it.
  const [inset, setInset] = useState(0);
  useEffect(() => {
    const viewport = window.visualViewport;
    if (!viewport) return;
    const update = () => setInset(keyboardInset(window.innerHeight, viewport));
    update();
    viewport.addEventListener("resize", update);
    viewport.addEventListener("scroll", update);
    return () => {
      viewport.removeEventListener("resize", update);
      viewport.removeEventListener("scroll", update);
    };
  }, []);
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
  // A risky shortcut (Ctrl+D, Ctrl+Z) waiting for its second press.
  const [armed, setArmed] = useState<string | null>(null);
  useEffect(() => {
    if (!armed) return;
    const timer = window.setTimeout(() => setArmed(null), 3000);
    return () => window.clearTimeout(timer);
  }, [armed]);
  const pressShortcut = (shortcut: TerminalShortcut) => {
    if (shortcut.risky && armed !== shortcut.label) {
      setArmed(shortcut.label);
      return;
    }
    setArmed(null);
    inputRef.current?.push(shortcut.value);
    // On a touch screen, don't bring back a keyboard the user put away.
    if (!touch || keyboardOpen) termRef.current?.focus();
  };
  // The provider recreates this function on every render; read it through
  // a ref so the terminal isn't rebuilt each time.
  const sendRef = useRef(sendTerminalInput);
  sendRef.current = sendTerminalInput;

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;
    let term: Terminal | undefined;
    let eventSource: EventSource | undefined;
    let resizeObserver: ResizeObserver | undefined;
    let frame = 0;

    void import("@xterm/xterm").then(({Terminal}) => {
      if (disposed) return;
      // Estimated until xterm has rendered a row we can measure.
      let rowHeight = FONT_SIZE * LINE_HEIGHT;
      const rowsFor = () => Math.max(10, Math.floor(container.clientHeight / rowHeight));
      term = new Terminal({
        cols: columnsRef.current,
        rows: rowsFor(),
        fontSize: FONT_SIZE,
        lineHeight: LINE_HEIGHT,
        fontFamily: 'Menlo, "DejaVu Sans Mono", Consolas, "Courier New", monospace',
        scrollback: 5000,
        cursorBlink: false,
        theme: {background: "#0b0d10", foreground: "#e6e6e6"},
      });
      term.open(container);
      // Measures the rendered cells, then sizes the ruler and the rows.
      const measure = () => {
        if (!term) return;
        const screen = container.querySelector(".xterm-screen");
        if (screen) setCellWidth(screen.getBoundingClientRect().width / term.cols);
        const firstRow = container.querySelector(".xterm-rows > div");
        if (firstRow && firstRow.getBoundingClientRect().height > 0) {
          rowHeight = firstRow.getBoundingClientRect().height;
        }
        if (term.rows !== rowsFor()) term.resize(term.cols, rowsFor());
      };
      measure();
      // Cell width per pixel of font size, from the first render.
      const firstScreen = container.querySelector(".xterm-screen");
      const cellWidthPerPx = firstScreen ? firstScreen.getBoundingClientRect().width / term.cols / FONT_SIZE : 0.6;
      fitRef.current = () => {
        const panel = panelRef.current;
        if (!term || !panel) return;
        if (term.cols !== columnsRef.current) term.resize(columnsRef.current, term.rows);
        const size = fitTerminalFontSize(panel.clientWidth - PANEL_PADDING, term.cols, cellWidthPerPx);
        if (term.options.fontSize !== size) term.options.fontSize = size;
        // xterm re-measures its cells on the next frame.
        window.requestAnimationFrame(measure);
      };
      fitRef.current();
      // The snapshot has no cursor position, so don't draw a cursor, and
      // have xterm wrap pastes so a multi-line paste isn't submitted line
      // by line.
      term.write("\x1b[?25l\x1b[?2004h");
      // The input xterm types through: tell phone keyboards it is a
      // terminal (no autocorrect or capitals) and track whether it has
      // focus, which is whether the on-screen keyboard is up.
      const textarea = container.querySelector<HTMLTextAreaElement>(".xterm-helper-textarea");
      if (textarea) {
        textarea.setAttribute("autocapitalize", "off");
        textarea.setAttribute("autocorrect", "off");
        textarea.setAttribute("spellcheck", "false");
        textarea.setAttribute("enterkeyhint", "enter");
        textarea.addEventListener("focus", () => setKeyboardOpen(true));
        textarea.addEventListener("blur", () => setKeyboardOpen(false));
      }
      // On a touch screen the keyboard opens when asked for (the keyboard
      // button, or a tap on the terminal), not as soon as TTY mode opens.
      if (!window.matchMedia("(pointer: coarse)").matches) term.focus();

      const input = new OrderedInput((data) => sendRef.current(data), (error) => {
        toast.error("Could not send keys to the agent", {
          description: error instanceof Error ? error.message : String(error),
        });
      });
      term.onData((data) => input.push(data));
      inputRef.current = input;
      termRef.current = term;

      // Redraw the latest snapshot at most once per frame, keeping the
      // reader's place if they scrolled up.
      let latest: string | undefined;
      const render = () => {
        frame = 0;
        if (!term || latest === undefined) return;
        const buffer = term.buffer.active;
        const fromBottom = buffer.baseY - buffer.viewportY;
        const screen = latest;
        latest = undefined;
        term.write(screenToTerminalOutput(screen), () => {
          if (term && fromBottom > 0) {
            term.scrollToLine(Math.max(0, term.buffer.active.baseY - fromBottom));
          }
        });
      };

      eventSource = new EventSource(`${storageScope}/internal/screen`);
      eventSource.onopen = () => setConnected(true);
      eventSource.onerror = () => setConnected(false);
      eventSource.addEventListener("screen", (event) => {
        try {
          latest = (JSON.parse(event.data) as {screen?: string}).screen ?? "";
        } catch {
          return;
        }
        if (!frame) frame = window.requestAnimationFrame(render);
      });

      resizeObserver = new ResizeObserver(() => fitRef.current());
      resizeObserver.observe(container);
      if (panelRef.current) resizeObserver.observe(panelRef.current);
    });

    return () => {
      disposed = true;
      if (frame) window.cancelAnimationFrame(frame);
      resizeObserver?.disconnect();
      eventSource?.close();
      term?.dispose();
      fitRef.current = () => {};
    };
  }, [storageScope]);

  // The server reports its terminal width after connecting.
  useEffect(() => {
    columnsRef.current = columns;
    fitRef.current();
  }, [columns]);

  // Keep the last lines in view when the keyboard opens or closes.
  useEffect(() => {
    termRef.current?.scrollToBottom();
  }, [inset, keyboardOpen]);

  return (
    <div className="flex min-h-0 flex-1 flex-col" style={inset ? {paddingBottom: inset} : undefined}>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-background/95 px-3 py-2 text-xs sm:px-4">
        <TerminalSquare className="size-4 shrink-0 text-muted-foreground" />
        <span className="font-semibold">TTY mode</span>
        <span className="hidden text-muted-foreground sm:inline">
          {connected ? "Keys go straight to the agent" : "Connecting to the terminal…"}
        </span>
        {!connected && <span className="text-muted-foreground sm:hidden">Connecting…</span>}
        <span className="hidden font-mono text-[11px] text-muted-foreground md:inline">
          {columns} columns · full-width characters take 2
        </span>
        <Button type="button" size="sm" variant="outline" className="ml-auto h-8" onClick={onExit}>
          <ArrowLeft />
          Back to chat
        </Button>
      </div>
      {/* mx-auto rather than flex centering: a centered flex child wider
          than a phone screen would be clipped on the left. */}
      <div ref={panelRef} className="min-h-0 flex-1 overflow-x-auto bg-[#0b0d10] p-2">
        <div className="mx-auto flex h-full w-fit flex-col">
          {cellWidth > 0 && (
            // Column ruler: a tick and label every 10 columns, so you can
            // tell whether odd-looking output is the agent's or ours.
            <div aria-hidden="true" className="relative mb-1 h-4 shrink-0 font-mono text-[10px] text-term-dim" style={{width: cellWidth * columns}}>
              {Array.from({length: Math.floor(columns / 10)}, (_, i) => (i + 1) * 10).map((column) => (
                <span key={column} className="absolute bottom-0 border-r border-term-dim/50 pr-1 leading-none" style={{right: (columns - column) * cellWidth}}>
                  {column}
                </span>
              ))}
            </div>
          )}
          <div ref={containerRef} className="min-h-0 flex-1" aria-label="Agent terminal" />
        </div>
      </div>
      {/* Keys a phone keyboard can't send. Buttons don't take focus, so the
          terminal (and the on-screen keyboard) stays active. */}
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
        <div className="flex min-w-0 flex-1 gap-1.5 overflow-x-auto" role="group" aria-label="Terminal keys">
          {terminalShortcuts.map((shortcut) => (
            <button
              key={shortcut.label}
              type="button"
              onMouseDown={(event) => event.preventDefault()}
              onClick={() => pressShortcut(shortcut)}
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
    </div>
  );
}
