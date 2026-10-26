"use client";

import {useEffect, useRef, useState} from "react";
import {ArrowLeft, TerminalSquare} from "lucide-react";
import {toast} from "sonner";
import {useChatActions, useChatSelector} from "./chat-provider";
import {Button} from "./ui/button";
import {TERMINAL_FONT_SIZE as FONT_SIZE, TERMINAL_LINE_HEIGHT as LINE_HEIGHT, TerminalKeyBar} from "./terminal-chrome";
import {LatestLinkChip} from "./terminal-links";
import {useXterm} from "./use-xterm";
import {fitTerminalFontSize, screenToTerminalOutput} from "@/lib/tty";
import {withCredentials} from "@/lib/auth";
import type {ScreenEvent} from "@/lib/events";

// The mirror uses the width of the agent's terminal (80 columns unless
// AgentAPI was started with --term-width), so lines wrap exactly as the
// agent drew them. The font scales to fill the window instead.
const DEFAULT_COLUMNS = 80;
// Horizontal padding of the terminal panel (p-2 on both sides).
const PANEL_PADDING = 16;

// TTY mode: an escape hatch for when the chat view looks wrong. It shows
// the agent's emulated terminal screen as-is and sends every keystroke
// straight to the agent, like attaching to the terminal.
export function TtyView({onExit}: {onExit: () => void}) {
  const storageScope = useChatSelector((chat) => chat.storageScope);
  const terminalColumns = useChatSelector((chat) => chat.terminalColumns);
  const {sendTerminalInput} = useChatActions();
  const columns = terminalColumns || DEFAULT_COLUMNS;
  const columnsRef = useRef(columns);
  const containerRef = useRef<HTMLDivElement>(null);
  const panelRef = useRef<HTMLDivElement>(null);
  // Re-fits the terminal to the panel; set once the terminal exists.
  const fitRef = useRef<() => void>(() => {});
  const [connected, setConnected] = useState(false);
  // Width of one terminal cell, for the column ruler.
  const [cellWidth, setCellWidth] = useState(0);
  // Estimated until xterm has rendered a row we can measure.
  const rowsFor = (container: HTMLElement, rowHeight = FONT_SIZE * LINE_HEIGHT) => Math.max(10, Math.floor(container.clientHeight / rowHeight));

  const {termRef, inputRef, link, modifier, setModifier, touch, keyboardOpen} = useXterm({
    containerRef,
    key: storageScope,
    options: (container) => ({cols: columnsRef.current, rows: rowsFor(container), scrollback: 5000, cursorBlink: false}),
    send: sendTerminalInput,
    onSendError: (error) => {
      toast.error("Could not send keys to the agent", {
        description: error instanceof Error ? error.message : String(error),
      });
    },
    connect: (term, container) => {
      let rowHeight = FONT_SIZE * LINE_HEIGHT;
      let frame = 0;
      // Measures the rendered cells, then sizes the ruler and the rows.
      const measure = () => {
        const screen = container.querySelector(".xterm-screen");
        if (screen) setCellWidth(screen.getBoundingClientRect().width / term.cols);
        const firstRow = container.querySelector(".xterm-rows > div");
        if (firstRow && firstRow.getBoundingClientRect().height > 0) {
          rowHeight = firstRow.getBoundingClientRect().height;
        }
        if (term.rows !== rowsFor(container, rowHeight)) term.resize(term.cols, rowsFor(container, rowHeight));
      };
      measure();
      // Cell width per pixel of font size, from the first render.
      const firstScreen = container.querySelector(".xterm-screen");
      const cellWidthPerPx = firstScreen ? firstScreen.getBoundingClientRect().width / term.cols / FONT_SIZE : 0.6;
      fitRef.current = () => {
        const panel = panelRef.current;
        if (!panel) return;
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

      // Redraw the latest snapshot at most once per frame, keeping the
      // reader's place if they scrolled up.
      let latest: string | undefined;
      const render = () => {
        frame = 0;
        if (latest === undefined) return;
        const buffer = term.buffer.active;
        const fromBottom = buffer.baseY - buffer.viewportY;
        const screen = latest;
        latest = undefined;
        term.write(screenToTerminalOutput(screen), () => {
          if (fromBottom > 0) {
            term.scrollToLine(Math.max(0, term.buffer.active.baseY - fromBottom));
          }
        });
      };

      const eventSource = new EventSource(`${storageScope}/internal/screen`, {
        withCredentials: withCredentials(storageScope),
      });
      eventSource.onopen = () => setConnected(true);
      eventSource.onerror = () => setConnected(false);
      eventSource.addEventListener("screen", (event) => {
        try {
          latest = (JSON.parse(event.data) as Partial<ScreenEvent>).screen ?? "";
        } catch {
          return;
        }
        if (!frame) frame = window.requestAnimationFrame(render);
      });

      const resizeObserver = new ResizeObserver(() => fitRef.current());
      resizeObserver.observe(container);
      if (panelRef.current) resizeObserver.observe(panelRef.current);
      return () => {
        if (frame) window.cancelAnimationFrame(frame);
        resizeObserver.disconnect();
        eventSource.close();
        fitRef.current = () => {};
      };
    },
  });

  // The server reports its terminal width after connecting.
  useEffect(() => {
    columnsRef.current = columns;
    fitRef.current();
  }, [columns]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
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
        <LatestLinkChip url={link} />
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
      <TerminalKeyBar
        termRef={termRef}
        touch={touch}
        keyboardOpen={keyboardOpen}
        modifier={modifier}
        onModifier={setModifier}
        onKeys={(data) => inputRef.current?.push(data)}
      />
    </div>
  );
}
