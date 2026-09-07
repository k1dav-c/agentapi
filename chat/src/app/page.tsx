import { Chat } from "@/components/chat";
import { ChatProvider } from "@/components/chat-provider";
import { Header } from "./header";
import { Suspense } from "react";

export default function Home() {
  return (
    <Suspense
      fallback={
        <div className="flex h-svh flex-col overflow-hidden bg-background">
          <div className="flex h-14 shrink-0 items-center justify-between border-b px-3 sm:h-16 sm:px-6">
            <div className="flex items-center gap-3">
              <div className="size-9 animate-pulse rounded-xl bg-muted" />
              <div className="space-y-1.5">
                <div className="h-4 w-24 animate-pulse rounded bg-muted" />
                <div className="hidden h-3 w-32 animate-pulse rounded bg-muted sm:block" />
              </div>
            </div>
            <div className="flex items-center gap-2">
              <div className="h-9 w-20 animate-pulse rounded-full bg-muted" />
              <div className="size-9 animate-pulse rounded-md bg-muted" />
            </div>
          </div>
          <div className="flex-1 p-6">
            <div className="mx-auto max-w-5xl space-y-4">
              <div className="h-20 animate-pulse rounded-2xl bg-muted/50" />
              <div className="h-32 animate-pulse rounded-2xl bg-muted/30" />
            </div>
          </div>
          <div className="shrink-0 border-t p-3">
            <div className="mx-auto max-w-5xl">
              <div className="h-14 animate-pulse rounded-2xl bg-muted/40" />
            </div>
          </div>
        </div>
      }
    >
      <ChatProvider>
        <main className="flex h-svh flex-col overflow-hidden bg-background">
          <Header />
          <Chat />
        </main>
      </ChatProvider>
    </Suspense>
  );
}
