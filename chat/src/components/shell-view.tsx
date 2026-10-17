"use client";

import {useEffect, useRef, useState} from "react";
import {ArrowLeft, RotateCw, TerminalSquare} from "lucide-react";
import {toast} from "sonner";
import type {Terminal} from "@xterm/xterm";
import {useChat} from "./chat-provider";
import {Button} from "./ui/button";
import {TerminalKeyBar, prepareTerminalInput, useTerminalKeyboard} from "./terminal-chrome";
import {OrderedInput} from "@/lib/tty";
import {decodeBase64, terminalSize} from "@/lib/shell";
import {withCredentials} from "@/lib/auth";
import {getErrorMessage} from "@/lib/error-utils";

const FONT_SIZE = 13;
const LINE_HEIGHT = 1.2;

type ShellState = "connecting" | "open" | "exited";

// The native transport's terminal: a shell in the agent's working
// directory, for logging the agent in or a quick command. Everyone watching
// shares it, and it keeps running while nobody does. command, if set, is
// typed into it once it is ready (the login card's "Run in terminal").
export function ShellView({onExit, command}: {onExit: () => void; command?: string}) {
  const {storageScope, terminalInput, terminalResize} = useChat();
  const containerRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const inputRef = useRef<OrderedInput | null>(null);
  const {touch, keyboardOpen, setKeyboardOpen, inset} = useTerminalKeyboard();
  const [state, setState] = useState<ShellState>("connecting");
  const [exitReason, setExitReason] = useState("");
  // Bumped to connect again, which starts a new shell after one exited.
  const [session, setSession] = useState(0);
  const commandRef = useRef(command);
  // The provider recreates these functions on every render; read them
  // through refs so the terminal isn't rebuilt each time.
  const inputFnRef = useRef(terminalInput);
  inputFnRef.current = terminalInput;
  const resizeFnRef = useRef(terminalResize);
  resizeFnRef.current = terminalResize;

  useEffect(() => {
    const container = containerRef.current;
    if (!container) return;
    let disposed = false;
    let term: Terminal | undefined;
    let eventSource: EventSource | undefined;
    let resizeObserver: ResizeObserver | undefined;
    let resizeTimer = 0;

    void import("@xterm/xterm").then(({Terminal}) => {
      if (disposed) return;
      term = new Terminal({
        fontSize: FONT_SIZE,
        lineHeight: LINE_HEIGHT,
        fontFamily: 'Menlo, "DejaVu Sans Mono", Consolas, "Courier New", monospace',
        scrollback: 10000,
        cursorBlink: true,
        theme: {background: "#0b0d10", foreground: "#e6e6e6"},
      });
      term.open(container);
      prepareTerminalInput(container, setKeyboardOpen);
      // On a touch screen the keyboard opens when asked for.
      if (!window.matchMedia("(pointer: coarse)").matches) term.focus();

      const input = new OrderedInput((data) => inputFnRef.current(data), (error) => {
        toast.error("Could not type into the terminal", {description: getErrorMessage(error)});
      });
      term.onData((data) => input.push(data));
      inputRef.current = input;
      termRef.current = term;

      // Fits the terminal to the panel, and tells the shell its size.
      let sent = "";
      const fit = () => {
        if (!term) return;
        const screen = container.querySelector(".xterm-screen");
        const row = container.querySelector(".xterm-rows > div");
        const cellWidth = screen ? screen.getBoundingClientRect().width / term.cols : 0;
        const cellHeight = row ? row.getBoundingClientRect().height : 0;
        const size = terminalSize(container.clientWidth, container.clientHeight, cellWidth, cellHeight);
        if (!size) return;
        if (size.cols !== term.cols || size.rows !== term.rows) term.resize(size.cols, size.rows);
        const key = `${size.cols}x${size.rows}`;
        if (key === sent) return;
        sent = key;
        window.clearTimeout(resizeTimer);
        resizeTimer = window.setTimeout(() => {
          resizeFnRef.current(size.cols, size.rows).catch(() => {
            // The next fit tries again; the shell keeps its last size.
            sent = "";
          });
        }, 150);
      };
      window.requestAnimationFrame(fit);
      resizeObserver = new ResizeObserver(() => fit());
      resizeObserver.observe(container);

      eventSource = new EventSource(`${storageScope}/terminal`, {
        withCredentials: withCredentials(storageScope),
      });
      eventSource.onopen = () => {
        // The stream starts with the recent output: replay it on a clean
        // terminal, also after a reconnect.
        term?.reset();
        setState("open");
        fit();
      };
      eventSource.onerror = () => setState((current) => (current === "exited" ? current : "connecting"));
      eventSource.addEventListener("output", (event) => {
        try {
          term?.write(decodeBase64((JSON.parse(event.data) as {data: string}).data));
        } catch {
          return;
        }
        // The shell is ready: type the command it was opened for.
        const pending = commandRef.current;
        if (pending) {
          commandRef.current = undefined;
          input.push(`${pending}\r`);
        }
      });
      eventSource.addEventListener("exit", (event) => {
        let reason = "";
        try {
          reason = (JSON.parse(event.data) as {reason?: string}).reason ?? "";
        } catch {
          // The reason is only shown.
        }
        // Don't reconnect: that would start a new shell right away.
        eventSource?.close();
        setExitReason(reason);
        setState("exited");
      });
    });

    return () => {
      disposed = true;
      window.clearTimeout(resizeTimer);
      resizeObserver?.disconnect();
      eventSource?.close();
      term?.dispose();
      termRef.current = null;
      inputRef.current = null;
    };
  }, [storageScope, session, setKeyboardOpen]);

  // Keep the last lines in view when the keyboard opens or closes.
  useEffect(() => {
    termRef.current?.scrollToBottom();
  }, [inset, keyboardOpen]);

  const restart = () => {
    setExitReason("");
    setState("connecting");
    setSession((n) => n + 1);
  };

  return (
    <div className="flex min-h-0 flex-1 flex-col" style={inset ? {paddingBottom: inset} : undefined} data-shell-view>
      <div className="flex shrink-0 flex-wrap items-center gap-2 border-b bg-background/95 px-3 py-2 text-xs sm:px-4">
        <TerminalSquare className="size-4 shrink-0 text-muted-foreground" />
        <span className="font-semibold">Terminal</span>
        <span className="hidden text-muted-foreground sm:inline">
          {state === "open" ? "A shell in the agent's working directory" : state === "connecting" ? "Connecting…" : "The shell exited"}
        </span>
        {state !== "open" && <span className="text-muted-foreground sm:hidden">{state === "connecting" ? "Connecting…" : "Exited"}</span>}
        <Button type="button" size="sm" variant="outline" className="ml-auto h-8" onClick={onExit}>
          <ArrowLeft />
          Back to chat
        </Button>
      </div>
      <div className="relative min-h-0 flex-1 bg-[#0b0d10] p-2">
        <div ref={containerRef} className="h-full w-full" aria-label="Terminal" />
        {state === "exited" && (
          <div className="absolute inset-x-0 bottom-0 flex flex-wrap items-center justify-center gap-3 border-t border-white/10 bg-[#0b0d10]/95 px-4 py-3 text-xs text-[#e6e6e6]">
            <span>{exitReason || "The shell exited."}</span>
            <Button type="button" size="sm" variant="secondary" className="h-8" onClick={restart}>
              <RotateCw />
              Start a new shell
            </Button>
          </div>
        )}
      </div>
      <TerminalKeyBar termRef={termRef} touch={touch} keyboardOpen={keyboardOpen} onKeys={(data) => inputRef.current?.push(data)} />
    </div>
  );
}
