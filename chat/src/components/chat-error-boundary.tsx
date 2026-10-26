"use client";

import {Component, useState, type ErrorInfo, type ReactNode} from "react";
import {RefreshCw, Trash2} from "lucide-react";
import {Button} from "./ui/button";
import {resolveAgentAPIUrl} from "@/lib/agentapi-url";
import {getErrorMessage} from "@/lib/error-utils";
import {clearSession} from "@/lib/session-cache";

// ChatErrorBoundary shows what went wrong when the chat throws while it
// renders, instead of leaving a blank page. Most of what the chat shows was
// cached for the next visit, so if the cache holds what it can't show, a
// reload fails the same way: the error screen offers to clear the cached
// transcript of this server before it reloads.
export class ChatErrorBoundary extends Component<{children: ReactNode}, {failed: boolean; error: unknown}> {
  state = {failed: false, error: undefined as unknown};

  static getDerivedStateFromError(error: unknown) {
    return {failed: true, error};
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error("The chat stopped:", error, info.componentStack);
  }

  render() {
    return this.state.failed ? <ChatErrorScreen error={this.state.error} /> : this.props.children;
  }
}

export function ChatErrorScreen({error}: {error: unknown}) {
  const [clearing, setClearing] = useState(false);
  const clearAndReload = async () => {
    setClearing(true);
    // The page's own server, as the chat found it.
    const scope = resolveAgentAPIUrl(new URLSearchParams(window.location.search).get("url"));
    if (scope) await clearSession(scope);
    window.location.reload();
  };
  return (
    <main className="app-frame flex flex-col overflow-y-auto bg-background">
      <div role="alert" className="mx-auto flex min-h-full w-full max-w-[49rem] flex-col justify-center px-6 py-12" data-chat-error>
        <p className="font-mono text-[11px] uppercase tracking-[0.08em] text-muted-foreground">Error</p>
        <h2 className="mt-2 text-balance text-3xl font-semibold tracking-[-0.02em] sm:text-4xl">The chat stopped working</h2>
        <p className="mt-3 max-w-[34rem] text-pretty text-[15px] leading-[1.65] text-muted-foreground">
          It ran into something it can&apos;t show. If reloading shows this again, clear the transcript this browser
          keeps for the server: the chat then reads the conversation from the server again. The conversation
          itself stays on the server.
        </p>
        <pre className="mt-6 max-h-60 overflow-auto whitespace-pre-wrap break-words rounded-xl border bg-muted/40 p-3 font-mono text-xs text-destructive" data-error-text>
          {getErrorMessage(error)}
        </pre>
        <div className="mt-6 flex flex-wrap gap-2">
          <Button type="button" onClick={() => void clearAndReload()} disabled={clearing}>
            <Trash2 />
            Clear the cached transcript and reload
          </Button>
          <Button type="button" variant="outline" onClick={() => window.location.reload()} disabled={clearing}>
            <RefreshCw />
            Reload
          </Button>
        </div>
      </div>
    </main>
  );
}
