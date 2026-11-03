"use client";

import {useState, type Dispatch, type SetStateAction} from "react";
import {toast} from "sonner";
import type {createChatAPI} from "@/lib/chat-api";
import {isDraftMessage, type DraftMessage, type Message} from "@/lib/conversation";
import type {DispatchMode} from "@/lib/dispatch";
import {getErrorMessage} from "@/lib/error-utils";
import {parseFailedMessages} from "@/lib/failed-messages";
import {usePersisted} from "@/lib/use-persisted";

export interface SendResult {
  ok: boolean;
  queued: boolean;
  // The message was added to the turn the agent is working on.
  steered?: boolean;
}

// useDrafts sends the user's messages. A user message shows at once as a
// draft, "sending" until the server has it (messagesAfter drops it for the
// server's message, sendMessage once the server answered), and "failed"
// when the send failed, to be retried or dismissed; failed drafts survive a
// reload. loading is set while a user message is on its way.
export function useDrafts({
  agentAPIUrl,
  api,
  messages,
  setMessages,
  queueChanged,
}: {
  agentAPIUrl: string;
  api: ReturnType<typeof createChatAPI>;
  messages: (Message | DraftMessage)[];
  setMessages: Dispatch<SetStateAction<(Message | DraftMessage)[]>>;
  // Reads the queue again: the message may have been queued.
  queueChanged: () => Promise<void>;
}) {
  const [loading, setLoading] = useState(false);

  usePersisted(
    `agentapi.chat.failed-messages:${agentAPIUrl}`,
    messages,
    (current) => {
      const failed = current.filter((message): message is DraftMessage => isDraftMessage(message) && message.deliveryStatus === "failed");
      return failed.length > 0 ? JSON.stringify(failed) : null;
    },
    (stored) => {
      if (!stored) return;
      const failed = parseFailedMessages(stored) as DraftMessage[];
      setMessages((previous) => {
        const existing = new Set(previous.filter(isDraftMessage).map((message) => message.clientId));
        return [...previous, ...failed.filter((message) => !existing.has(message.clientId))];
      });
    },
  );

  const withoutDraft = (clientId: string) => (previous: (Message | DraftMessage)[]) =>
    previous.filter((message) => !isDraftMessage(message) || message.clientId !== clientId);

  // Sends a message; a raw one (keys for the terminal) without a draft.
  const sendMessage = async (content: string, type: "user" | "raw" = "user", mode: DispatchMode = "queue"): Promise<SendResult> => {
    // For user messages, require non-empty content
    if (type === "user" && !content.trim()) return {ok: false, queued: false};
    const clientId = crypto.randomUUID();
    if (type === "user") {
      setMessages((previous) => [...previous, {role: "user", content, clientId, deliveryStatus: "sending"}]);
      setLoading(true);
    }
    try {
      const result = await api.sendMessage(content, type, mode);
      await queueChanged();
      if (type === "user") setMessages(withoutDraft(clientId));
      return {ok: result.ok, queued: result.queued, steered: result.steered};
    } catch (error) {
      console.error("Error sending message:", error);
      const message = getErrorMessage(error);
      toast.error(`Error sending message`, {description: message});
      if (type === "user") {
        setMessages((previous) =>
          previous.map((item) => (isDraftMessage(item) && item.clientId === clientId ? {...item, deliveryStatus: "failed", error: message} : item)),
        );
      }
      return {ok: false, queued: false};
    } finally {
      if (type === "user") setLoading(false);
    }
  };

  const dismissFailedMessage = (clientId: string) => setMessages(withoutDraft(clientId));

  const retryFailedMessage = async (clientId: string) => {
    const failedMessage = messages.find((message) => isDraftMessage(message) && message.clientId === clientId);
    if (!failedMessage) return false;
    dismissFailedMessage(clientId);
    const result = await sendMessage(failedMessage.content, "user");
    return result.ok;
  };

  return {loading, sendMessage, retryFailedMessage, dismissFailedMessage};
}
