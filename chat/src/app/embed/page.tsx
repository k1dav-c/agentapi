import { Chat } from "@/components/chat";
import { ChatProvider } from "@/components/chat-provider";
import { EmbedStatusBar } from "@/components/embed-status-bar";
import { Suspense } from "react";

export default function EmbedPage() {
  return (
    <Suspense
      fallback={
        <div className="text-center p-4 text-sm">Loading chat interface...</div>
      }
    >
      <ChatProvider>
        <main className="flex h-svh flex-col overflow-hidden bg-background">
          <EmbedStatusBar />
          <Chat />
        </main>
      </ChatProvider>
    </Suspense>
  );
}
