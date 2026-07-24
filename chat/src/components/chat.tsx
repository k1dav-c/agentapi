"use client";

import {useChat} from "./chat-provider";
import MessageInput from "./message-input";
import MessageList from "./message-list";

export function Chat() {
  const { messages, loading, sendMessage, serverStatus, agentType } = useChat();

  return (
    <section className="relative flex min-h-0 flex-1 flex-col bg-[radial-gradient(circle_at_top,_var(--surface-glow),_transparent_42%)]">
      <MessageList
        messages={messages}
        serverStatus={serverStatus}
        agentType={agentType}
      />
      <MessageInput
        onSendMessage={sendMessage}
        disabled={loading}
        serverStatus={serverStatus}
      />
    </section>
  );
}
