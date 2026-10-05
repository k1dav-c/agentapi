"use client";

import { useEffect, useState } from "react";
import { ImageOff } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogTitle, DialogTrigger } from "./ui/dialog";
import { useChat } from "./chat-provider";
import { attachedPaths, uploadedImageURL } from "@/lib/uploads";

// A thumbnail of an uploaded image that opens it full size. Uploads live in
// the server's temp directory, so an old one can be gone (for example after
// the workspace restarted); then it says so instead of a broken image.
export function ImageThumb({ src, name, className = "size-20" }: { src: string; name: string; className?: string }) {
  const [failed, setFailed] = useState(false);
  if (failed) {
    return (
      <span
        className={`grid shrink-0 place-items-center rounded-lg border bg-muted/40 p-1 text-center text-[10px] leading-tight text-muted-foreground ${className}`}
        title={`${name} is no longer available`}
      >
        <ImageOff className="size-4" />
        <span className="sr-only">{name} is no longer available</span>
      </span>
    );
  }
  return (
    <Dialog>
      <DialogTrigger asChild>
        <button
          type="button"
          className={`shrink-0 overflow-hidden rounded-lg border bg-muted/40 outline-none transition hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring ${className}`}
          aria-label={`Open ${name}`}
          title={name}
        >
          {/* eslint-disable-next-line @next/next/no-img-element -- served by AgentAPI, not optimizable */}
          <img src={src} alt={name} className="size-full object-cover" loading="lazy" onError={() => setFailed(true)} />
        </button>
      </DialogTrigger>
      <DialogContent className="flex max-h-[90dvh] w-auto max-w-[min(64rem,calc(100vw-2rem))] flex-col gap-2 p-3 sm:max-w-[min(64rem,calc(100vw-2rem))]">
        <DialogTitle className="truncate pr-8 text-sm font-medium">{name}</DialogTitle>
        <DialogDescription className="sr-only">Uploaded image</DialogDescription>
        {/* eslint-disable-next-line @next/next/no-img-element -- served by AgentAPI, not optimizable */}
        <img src={src} alt={name} className="min-h-0 max-w-full flex-1 rounded-md object-contain" />
      </DialogContent>
    </Dialog>
  );
}

// The preview URL for a file that is still in the browser (being uploaded
// or just pasted), released when it is no longer shown.
export function useObjectURL(file: File | undefined): string | undefined {
  const [url, setURL] = useState<string>();
  useEffect(() => {
    if (!file) return;
    const created = URL.createObjectURL(file);
    setURL(created);
    return () => {
      URL.revokeObjectURL(created);
      setURL(undefined);
    };
  }, [file]);
  return url;
}

// Thumbnails of the uploaded images a message refers to (@"<path>").
export function MessageImages({ content }: { content: string }) {
  const { storageScope } = useChat();
  const images = attachedPaths(content)
    .map((path) => ({ path, src: uploadedImageURL(storageScope, path) }))
    .filter((image): image is { path: string; src: string } => image.src !== undefined);
  if (images.length === 0) return null;
  return (
    <div className="mt-2.5 flex flex-wrap gap-2" aria-label="Attached images">
      {images.map((image) => (
        <ImageThumb key={image.path} src={image.src} name={image.path.split("/").pop() ?? image.path} className="size-24" />
      ))}
    </div>
  );
}
