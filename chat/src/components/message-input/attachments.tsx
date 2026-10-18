"use client";

import {useEffect, useRef, useState} from "react";
import {Paperclip, RefreshCw, X} from "lucide-react";
import {Button} from "../ui/button";
import {ImageThumb, useObjectURL} from "../image-preview";
import type {FileUploadResponse} from "../chat-provider";
import type {UploadOptions} from "@/lib/chat-api";
import {isImageFile, uploadedImageURL} from "@/lib/uploads";
import {parsePersistedAttachments, removeAttachmentToken} from "@/lib/attachment-state";
import {usePersisted} from "@/lib/use-persisted";

export const MAX_UPLOAD_SIZE = 10 * 1024 * 1024;

export interface Attachment {
  id: string;
  name: string;
  size: number;
  file?: File;
  progress: number;
  status: "uploading" | "completed" | "failed";
  filePath?: string;
  error?: string;
}

// useAttachments uploads the files added to the composer. An uploaded
// file's @"<path>" joins the message (setMessage); removing it takes it out
// again. The uploaded ones are kept with the draft under storageKey, so a
// reload doesn't lose them.
export function useAttachments({
  storageKey,
  uploadFiles,
  setMessage,
}: {
  storageKey: string;
  uploadFiles: (formData: FormData, options?: UploadOptions) => Promise<FileUploadResponse>;
  setMessage: (update: (message: string) => string) => void;
}) {
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const controllers = useRef(new Map<string, AbortController>());

  usePersisted(
    storageKey,
    attachments,
    (current) => {
      const completed = current
        .filter((attachment): attachment is Attachment & {filePath: string} =>
          attachment.status === "completed" && typeof attachment.filePath === "string",
        )
        .map(({id, name, size, filePath}) => ({id, name, size, filePath}));
      return completed.length > 0 ? JSON.stringify(completed) : null;
    },
    (stored) =>
      setAttachments(
        parsePersistedAttachments(stored).map((attachment) => ({...attachment, progress: 100, status: "completed"})),
      ),
  );

  useEffect(() => {
    const uploads = controllers.current;
    return () => {
      uploads.forEach((controller) => controller.abort());
      uploads.clear();
    };
  }, []);

  const update = (id: string, change: Partial<Attachment>) =>
    setAttachments((previous) => previous.map((item) => (item.id === id ? {...item, ...change} : item)));

  const upload = async (attachment: Attachment) => {
    if (!attachment.file) return;
    const controller = new AbortController();
    controllers.current.set(attachment.id, controller);
    update(attachment.id, {status: "uploading", progress: 0, error: undefined});

    const formData = new FormData();
    formData.append("file", attachment.file, attachment.name);
    const response = await uploadFiles(formData, {
      signal: controller.signal,
      onProgress: (progress) => update(attachment.id, {progress}),
    });
    controllers.current.delete(attachment.id);

    if (response.ok && response.filePath) {
      update(attachment.id, {status: "completed", progress: 100, filePath: response.filePath});
      setMessage((current) => `${current} @"${response.filePath}"`);
    } else if (!controller.signal.aborted) {
      update(attachment.id, {status: "failed", error: response.error ?? "Upload failed."});
    }
  };

  const add = (files: File[]) => {
    const added: Attachment[] = files.map((file) => ({
      id: crypto.randomUUID(),
      name: file.name,
      size: file.size,
      file,
      progress: 0,
      status: file.size > MAX_UPLOAD_SIZE ? "failed" : "uploading",
      error: file.size > MAX_UPLOAD_SIZE ? "File exceeds the 10 MB upload limit." : undefined,
    }));
    setAttachments((previous) => [...previous, ...added]);
    added.filter((attachment) => attachment.status === "uploading").forEach((attachment) => void upload(attachment));
  };

  const remove = (attachment: Attachment) => {
    controllers.current.get(attachment.id)?.abort();
    controllers.current.delete(attachment.id);
    setAttachments((previous) => previous.filter((item) => item.id !== attachment.id));
    if (attachment.filePath) {
      setMessage((current) => removeAttachmentToken(current, attachment.filePath!));
    }
  };

  // The uploaded attachments went out with the message.
  const clearSent = () => setAttachments((previous) => previous.filter((attachment) => attachment.status !== "completed"));

  return {
    attachments,
    uploading: attachments.some((attachment) => attachment.status === "uploading"),
    add,
    retry: (attachment: Attachment) => void upload(attachment),
    remove,
    clearSent,
  };
}

// The composer's attachments, with their upload progress or failure.
export function AttachmentList({
  attachments,
  baseURL,
  onRetry,
  onRemove,
}: {
  attachments: Attachment[];
  baseURL: string;
  onRetry: (attachment: Attachment) => void;
  onRemove: (attachment: Attachment) => void;
}) {
  return (
    <div
      className="space-y-1.5 border-t bg-muted/15 px-3 py-2"
      aria-label="Attachments"
      aria-live="polite"
    >
      {attachments.map((attachment) => (
        <div
          key={attachment.id}
          className="flex min-h-10 items-center gap-2 rounded-lg border bg-background px-2.5 py-1.5 text-xs"
        >
          <AttachmentIcon attachment={attachment} baseURL={baseURL} />
          <div className="min-w-0 flex-1">
            <div className="flex items-center justify-between gap-2">
              <span className="truncate font-medium">
                {attachment.name}
              </span>
              <span className="shrink-0 text-[10px] text-muted-foreground">
                {formatFileSize(attachment.size)}
              </span>
            </div>
            {attachment.status === "uploading" ? (
              <div className="mt-1 flex items-center gap-2">
                <div className="h-1 flex-1 overflow-hidden rounded-full bg-muted">
                  <div
                    className="h-full rounded-full bg-primary transition-[width]"
                    style={{width: `${attachment.progress}%`}}
                  />
                </div>
                <span className="w-8 text-right text-[10px] text-muted-foreground">
                  {attachment.progress}%
                </span>
              </div>
            ) : (
              <p
                className={
                  attachment.status === "failed"
                    ? "truncate text-[10px] text-destructive"
                    : "text-[10px] text-status-success"
                }
              >
                {attachment.status === "failed"
                  ? attachment.error
                  : "Attached"}
              </p>
            )}
          </div>
          {attachment.status === "failed" &&
            attachment.file &&
            attachment.size <= MAX_UPLOAD_SIZE && (
              <Button
                type="button"
                size="icon"
                variant="ghost"
                className="size-8"
                onClick={() => onRetry(attachment)}
                title="Retry upload"
              >
                <RefreshCw />
                <span className="sr-only">Retry upload</span>
              </Button>
            )}
          <Button
            type="button"
            size="icon"
            variant="ghost"
            className="size-8"
            onClick={() => onRemove(attachment)}
            title={
              attachment.status === "uploading"
                ? "Cancel upload"
                : "Remove attachment"
            }
          >
            <X />
            <span className="sr-only">
              {attachment.status === "uploading"
                ? "Cancel upload"
                : "Remove attachment"}
            </span>
          </Button>
        </div>
      ))}
      <p className="px-1 text-[10px] text-muted-foreground">
        Maximum file size: 10 MB per file.
      </p>
    </div>
  );
}

function formatFileSize(bytes: number) {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

// An image attachment shows a thumbnail: from the browser while it uploads
// (or after a paste), then from the server.
function AttachmentIcon({attachment, baseURL}: {attachment: Attachment; baseURL: string}) {
  const image = isImageFile({name: attachment.name, type: attachment.file?.type});
  const local = useObjectURL(image ? attachment.file : undefined);
  const src = local ?? (attachment.filePath ? uploadedImageURL(baseURL, attachment.filePath) : undefined);
  if (!image || !src) return <Paperclip className="size-3.5 shrink-0 text-muted-foreground" />;
  return <ImageThumb src={src} name={attachment.name} className="size-9" />;
}
