export interface PersistedAttachment {
  id: string;
  name: string;
  size: number;
  filePath: string;
}

export function parsePersistedAttachments(value: string | null) {
  if (!value) return [];
  try {
    const parsed = JSON.parse(value) as unknown;
    if (!Array.isArray(parsed)) return [];
    return parsed.filter(
      (item): item is PersistedAttachment =>
        typeof item === "object" &&
        item !== null &&
        typeof (item as PersistedAttachment).id === "string" &&
        typeof (item as PersistedAttachment).name === "string" &&
        typeof (item as PersistedAttachment).size === "number" &&
        typeof (item as PersistedAttachment).filePath === "string",
    );
  } catch {
    return [];
  }
}

export function removeAttachmentToken(message: string, filePath: string) {
  return message.replace(` @"${filePath}"`, "");
}
