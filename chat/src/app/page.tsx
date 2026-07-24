import { Chat } from "@/components/chat";
import { ChatProvider } from "@/components/chat-provider";
import { Header } from "./header";
import { Suspense } from "react";

export default function Home() {
  return (
    <Suspense
      fallback={
        <div className="text-center p-4 text-sm">Loading chat interface...</div>
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
