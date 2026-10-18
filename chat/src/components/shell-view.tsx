"use client";

import {useRef, useState} from "react";
import {ArrowLeft, RotateCw, TerminalSquare} from "lucide-react";
import {toast} from "sonner";
import {useChat} from "./chat-provider";
import {Button} from "./ui/button";
import {TerminalKeyBar} from "./terminal-chrome";
import {useXterm} from "./use-xterm";
import {decodeBase64, terminalSize} from "@/lib/shell";
import {withCredentials} from "@/lib/auth";
import {getErrorMessage} from "@/lib/error-utils";

type ShellState = "connecting" | "open" | "exited";

// The native transport's terminal: a shell in the agent's working
// directory, for logging the agent in or a quick command. Everyone watching
// shares it, and it keeps running while nobody does. command, if set, is
// typed into it once it is ready (the login card's "Run in terminal").
export function ShellView({onExit, command}: {onExit: () => void; command?: string}) {
  const {storageScope, terminalInput, terminalResize} = useChat();
  const containerRef = useRef<HTMLDivElement>(null);
  const [state, setState] = useState<ShellState>("connecting");
  const [exitReason, setExitReason] = useState("");
  // Bumped to connect again, which starts a new shell after one exited.
  const [session, setSession] = useState(0);
  const commandRef = useRef(command);
  // Whether the shell runs: keys typed after it exited go nowhere.
  const runningRef = useRef(false);

  const {termRef, inputRef, touch, keyboardOpen, inset} = useXterm({
    containerRef,
    key: `${storageScope}#${session}`,
    options: () => ({scrollback: 10000, cursorBlink: true}),
    send: (data) => (runningRef.current ? terminalInput(data) : undefined),
    onSendError: (error) => toast.error("Could not type into the terminal", {description: getErrorMessage(error)}),
    connect: (term, container) => {
      let resizeTimer = 0;
      // Fits the terminal to the panel, and tells the shell its size.
      let sent = "";
      const fit = () => {
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
          terminalResize(size.cols, size.rows).catch(() => {
            // The next fit tries again; the shell keeps its last size.
            sent = "";
          });
        }, 150);
      };
      const frame = window.requestAnimationFrame(fit);
      const resizeObserver = new ResizeObserver(() => fit());
      resizeObserver.observe(container);

      const eventSource = new EventSource(`${storageScope}/terminal`, {
        withCredentials: withCredentials(storageScope),
      });
      eventSource.onopen = () => {
        // The stream starts with the recent output: replay it on a clean
        // terminal, also after a reconnect.
        term.reset();
        runningRef.current = true;
        setState("open");
        fit();
      };
      eventSource.onerror = () => setState((current) => (current === "exited" ? current : "connecting"));
      eventSource.addEventListener("output", (event) => {
        try {
          term.write(decodeBase64((JSON.parse(event.data) as {data: string}).data));
        } catch {
          return;
        }
        // The shell is ready: type the command it was opened for.
        const pending = commandRef.current;
        if (pending) {
          commandRef.current = undefined;
          inputRef.current?.push(`${pending}\r`);
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
        eventSource.close();
        runningRef.current = false;
        setExitReason(reason);
        setState("exited");
      });
      return () => {
        runningRef.current = false;
        window.cancelAnimationFrame(frame);
        window.clearTimeout(resizeTimer);
        resizeObserver.disconnect();
        eventSource.close();
      };
    },
  });

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
      <TerminalKeyBar
        termRef={termRef}
        touch={touch}
        keyboardOpen={keyboardOpen}
        onKeys={(data) => {
          if (runningRef.current) inputRef.current?.push(data);
        }}
      />
    </div>
  );
}
