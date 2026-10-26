import { Chat } from "@/components/chat";
import {ChatProvider} from "@/components/chat-provider";
import {ChatErrorBoundary} from "@/components/chat-error-boundary";
import { FileViewerProvider } from "@/components/file-viewer";
import { EmbedBar } from "@/components/embed-bar";
import { Suspense } from "react";

export default function EmbedPage() {
  return (
    <Suspense
      fallback={
        <div className="text-center p-4 text-sm">Loading chat interface...</div>
      }
    >
      <ChatErrorBoundary>
        <ChatProvider>
          <FileViewerProvider>
            <main className="fixed inset-0 flex flex-col overflow-hidden bg-background">
              <EmbedBar />
              <Chat />
            </main>
          </FileViewerProvider>
        </ChatProvider>
      </ChatErrorBoundary>
    </Suspense>
  );
}
