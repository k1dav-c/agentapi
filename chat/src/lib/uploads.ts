// Files uploaded with POST /upload are referenced in a message as
// @"<path>", where the path is <upload dir>/<checksum>/<name>. Images among
// them can be shown through GET /uploads/<checksum>/<name>.

const uploadedPathRe = /\/agentapi-uploads-[^/]+\/([0-9a-f]{16})\/([^/"]+)$/;
const imageNameRe = /\.(png|jpe?g|gif|webp|bmp)$/i;

// The URL that shows an uploaded image, or undefined when the path isn't an
// image this server stored.
export function uploadedImageURL(baseURL: string, filePath: string): string | undefined {
  const match = filePath.match(uploadedPathRe);
  if (!match || !imageNameRe.test(match[2])) return undefined;
  return `${baseURL}/uploads/${match[1]}/${encodeURIComponent(match[2])}`;
}

// The @"..." file references in a message, in order.
export function attachedPaths(message: string): string[] {
  return [...message.matchAll(/@"([^"]+)"/g)].map((match) => match[1]);
}

export function isImageFile(file: {name: string; type?: string}): boolean {
  return (file.type ?? "").startsWith("image/") || imageNameRe.test(file.name);
}

const extensionFor: Record<string, string> = {
  "image/png": "png",
  "image/jpeg": "jpg",
  "image/gif": "gif",
  "image/webp": "webp",
  "image/bmp": "bmp",
};

// A name for a pasted file. Clipboards name screenshots "image.png" (or
// nothing), which says nothing once several are in a conversation.
export function pastedFileName(name: string, type: string, now: Date): string {
  if (name && !/^image\.\w+$/i.test(name)) return name;
  const pad = (value: number) => String(value).padStart(2, "0");
  const stamp = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
  const extension = extensionFor[type] ?? name.split(".").pop() ?? "png";
  return `pasted-${stamp}.${extension}`;
}

// Files to upload from a paste. Text wins: copying cells or rich text often
// puts an image of it on the clipboard too, and then the text is what was
// meant.
export function filesToUploadFromPaste(clipboard: {getData(type: string): string; files: ArrayLike<File>}): File[] {
  if (clipboard.getData("text/plain").trim() !== "") return [];
  return Array.from(clipboard.files);
}

// The message as shown under its image thumbnails: the @"..." references
// to uploaded images are dropped (the thumbnails stand for them). Only for
// display; the message the agent got, copies and search keep them.
export function withoutUploadedImages(message: string): string {
  return message
    .replace(/[ \t]*@"([^"]+)"/g, (token, path: string) => (uploadedImageURL("", path) ? "" : token))
    .trim();
}
