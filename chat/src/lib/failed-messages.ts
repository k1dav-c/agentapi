export interface PersistedFailedMessage {
  clientId: string;
  role: "user";
  content: string;
  deliveryStatus: "failed";
  error?: string;
}

export function parseFailedMessages(value: string | null) {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (message): message is PersistedFailedMessage =>
        typeof message === "object" &&
        message !== null &&
        typeof (message as PersistedFailedMessage).clientId === "string" &&
        typeof (message as PersistedFailedMessage).content === "string" &&
        (message as PersistedFailedMessage).role === "user" &&
        (message as PersistedFailedMessage).deliveryStatus === "failed",
    );
  } catch {
    return [];
  }
}
