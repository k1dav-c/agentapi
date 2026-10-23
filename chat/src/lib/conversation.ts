// The conversation as the chat holds it: the server's messages, the
// drafts it shows while a message is sent, and the agent's structured
// (rich) messages.

export interface Message {
  id: number;
  role: string;
  content: string;
  time?: string;
}

// Draft messages are used to optimistically update the UI
// before the server responds.
export interface DraftMessage extends Omit<Message, "id"> {
  id?: number;
  clientId: string;
  deliveryStatus: "sending" | "failed";
  error?: string;
}

export interface RichContentBlock {
  type: "text" | "thinking" | "tool_use" | "tool_result";
  text?: string;
  thinking?: string;
  tool_use_id?: string;
  tool_name?: string;
  tool_input?: unknown;
  status?: "running" | "completed" | "failed";
  is_error?: boolean;
  // Images in a tool result (an image file the agent read), served by
  // AgentAPI at url.
  images?: {media_type: string; url?: string}[];
}

export interface Usage {
  input_tokens: number;
  output_tokens: number;
  cache_creation_input_tokens: number;
  cache_read_input_tokens: number;
}

export interface RichMessage {
  message_id: string;
  role: string;
  content: RichContentBlock[];
  timestamp: string;
  usage?: Usage;
  model?: string;
  stop_reason?: string;
}

// A rich message as the server sends it. Go encodes an empty list as null:
// a message without content blocks has "content": null, a tool result
// without images "images": null.
export type RichMessagePayload = Omit<RichMessage, "content"> & {
  content: (Omit<RichContentBlock, "images"> & {images?: RichContentBlock["images"] | null})[] | null;
};

// normalizeRichMessage returns message with empty lists for null ones, so
// the rest of the chat can iterate them. A message that has none keeps its
// object. Every rich message that comes in (events, the cache, the REST
// API) goes through it: one with null content made the transcript throw,
// and as it was cached, it did again after every reload.
export function normalizeRichMessage<T extends RichMessagePayload>(message: T): Omit<T, "content"> & {content: RichContentBlock[]} {
  const content = Array.isArray(message.content) ? message.content : [];
  if (content === message.content && content.every((block) => block.images !== null)) {
    return message as Omit<T, "content"> & {content: RichContentBlock[]};
  }
  return {
    ...message,
    content: content.map((block) => (block.images === null ? {...block, images: []} : block) as RichContentBlock),
  };
}

// Whether message is a draft, which the server hasn't confirmed yet.
export function isDraftMessage(message: Message | DraftMessage): message is DraftMessage {
  return message.id === undefined;
}

// The last message of the user's: the task the agent works on, or worked
// on last.
export function lastUserMessage<T extends {role: string}>(messages: readonly T[]): T | undefined {
  return messages.findLast((message) => message.role === "user");
}

// The key of a rich message: its ID and role.
export const richMessageKey = (message: RichMessage) => `${message.message_id}\u0000${message.role}`;

// Drops the transport-only change sequence before an update is stored.
export function withoutSeq<T extends {seq?: number}>(item: T): Omit<T, "seq"> {
  const copy = {...item};
  delete copy.seq;
  return copy;
}
