import { Chat } from "@/components/chat";
import { ChatProvider } from "@/components/chat-provider";
import { FileViewerProvider } from "@/components/file-viewer";
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
        <FileViewerProvider>
          <main className="flex h-svh flex-col overflow-hidden bg-background">
            <EmbedStatusBar />
            <Chat />
          </main>
        </FileViewerProvider>
      </ChatProvider>
    </Suspense>
  );
}
