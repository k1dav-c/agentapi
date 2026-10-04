"use client";

import {useEffect, useRef, useState} from "react";
import {ArrowLeft, TerminalSquare} from "lucide-react";
import {toast} from "sonner";
import type {Terminal} from "@xterm/xterm";
import {useChat} from "./chat-provider";
import {Button} from "./ui/button";
import {OrderedInput, screenToTerminalOutput} from "@/lib/tty";

// AgentAPI runs the agent in an 80-column terminal by default; the mirror
// uses the same width so lines wrap exactly as the agent drew them.
const TTY_COLUMNS = 80;
const FONT_SIZE = 13;
const LINE_HEIGHT = 1.2;

// TTY mode: an escape hatch for when the chat view looks wrong. It shows
// the agent's emulated terminal screen as-is and sends every keystroke
// straight to the agent, like attaching to the terminal.
export function TtyView({onExit}: {onExit: () => void}) {
  const {storageScope, sendTerminalInput} = useChat();
  const containerRef = useRef<HTMLDivElement>(null);
  const [connected, setConnected] = useState(false);
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
        cols: TTY_COLUMNS,
        rows: rowsFor(),
        fontSize: FONT_SIZE,
        lineHeight: LINE_HEIGHT,
        fontFamily: 'Menlo, "DejaVu Sans Mono", Consolas, "Courier New", monospace',
        scrollback: 5000,
        cursorBlink: false,
        theme: {background: "#0b0d10", foreground: "#e6e6e6"},
      });
      term.open(container);
      const firstRow = container.querySelector(".xterm-rows > div");
      if (firstRow && firstRow.getBoundingClientRect().height > 0) {
        rowHeight = firstRow.getBoundingClientRect().height;
        term.resize(TTY_COLUMNS, rowsFor());
      }
      // The snapshot has no cursor position, so don't draw a cursor, and
      // have xterm wrap pastes so a multi-line paste isn't submitted line
      // by line.
      term.write("\x1b[?25l\x1b[?2004h");
      term.focus();

      const input = new OrderedInput((data) => sendRef.current(data), (error) => {
        toast.error("Could not send keys to the agent", {
          description: error instanceof Error ? error.message : String(error),
        });
      });
      term.onData((data) => input.push(data));

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

      resizeObserver = new ResizeObserver(() => {
        if (term && term.rows !== rowsFor()) term.resize(TTY_COLUMNS, rowsFor());
      });
      resizeObserver.observe(container);
    });

    return () => {
      disposed = true;
      if (frame) window.cancelAnimationFrame(frame);
      resizeObserver?.disconnect();
      eventSource?.close();
      term?.dispose();
    };
  }, [storageScope]);

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-background/95 px-3 py-2 text-xs sm:px-4">
        <TerminalSquare className="size-4 shrink-0 text-status-warning" />
        <span className="font-semibold">TTY mode</span>
        <span className="text-muted-foreground">
          {connected ? "Keys go straight to the agent" : "Connecting to the terminal…"}
        </span>
        <Button type="button" size="sm" variant="outline" className="ml-auto h-8" onClick={onExit}>
          <ArrowLeft />
          Back to chat
        </Button>
      </div>
      {/* mx-auto rather than flex centering: a centered flex child wider
          than a phone screen would be clipped on the left. */}
      <div className="min-h-0 flex-1 overflow-x-auto bg-[#0b0d10] p-2">
        <div ref={containerRef} className="mx-auto h-full w-fit" aria-label="Agent terminal" />
      </div>
    </div>
  );
}
