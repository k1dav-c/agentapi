"use client";

import {useState, FormEvent, KeyboardEvent, MouseEvent, useEffect, useRef, ChangeEvent} from "react";
import {Button} from "./ui/button";
import {Tooltip, TooltipTrigger, TooltipContent} from "./ui/tooltip";
import {
  SendIcon,
  Square,
  Paperclip,
  Mic,
  MicOff,
  LoaderCircle,
  ListPlus,
  Clock3,
  Check,
  Pencil,
  X,
  MoreHorizontal,
  RefreshCw,
} from "lucide-react";
import type {SendResult, ServerStatus} from "./chat-provider";
import TextareaAutosize from "react-textarea-autosize";
import {useChat} from "./chat-provider";
import {DragDrop} from "./drag-drop";
import {ImageThumb, useObjectURL} from "./image-preview";
import {filesToUploadFromPaste, isImageFile, pastedFileName, uploadedImageURL} from "@/lib/uploads";
import {toast} from "sonner";
import {getErrorMessage} from "@/lib/error-utils";
import {
  parsePersistedAttachments,
  removeAttachmentToken,
} from "@/lib/attachment-state";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";

interface MessageInputProps {
  onSendMessage: (message: string, type: "user" | "raw") => Promise<SendResult>;
  disabled?: boolean;
  serverStatus: ServerStatus;
  suggestedPrompt?: string;
  onSuggestedPromptApplied?: () => void;
  // Shown above the composer: the agent's state strip and decision card.
  dock?: React.ReactNode;
}

interface SpeechRecognitionEventLike extends Event {
  resultIndex: number;
  results: {
    [index: number]: {
      isFinal: boolean;
      [index: number]: { transcript: string };
    };
    length: number;
  };
}

interface SpeechRecognitionLike extends EventTarget {
  continuous: boolean;
  interimResults: boolean;
  lang: string;
  start: () => void;
  stop: () => void;
  abort: () => void;
  onresult: ((event: SpeechRecognitionEventLike) => void) | null;
  onend: (() => void) | null;
  onerror: ((event: Event & { error?: string }) => void) | null;
}

type SpeechRecognitionConstructor = new () => SpeechRecognitionLike;
const MAX_UPLOAD_SIZE = 10 * 1024 * 1024;

interface Attachment {
  id: string;
  name: string;
  size: number;
  file?: File;
  progress: number;
  status: "uploading" | "completed" | "failed";
  filePath?: string;
  error?: string;
}

export default function MessageInput({
  onSendMessage,
  disabled = false,
  serverStatus,
  suggestedPrompt = "",
  onSuggestedPromptApplied,
  dock,
}: MessageInputProps) {
  const [message, setMessage] = useState("");
  const [hydratedDraftKey, setHydratedDraftKey] = useState<string | null>(null);
  const [editingQueuedID, setEditingQueuedID] = useState<number | null>(null);
  const [editingQueuedMessage, setEditingQueuedMessage] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [isStopping, setIsStopping] = useState(false);
  const [isListening, setIsListening] = useState(false);
  const [speechSupported, setSpeechSupported] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const recognitionRef = useRef<SpeechRecognitionLike | null>(null);
  const speechBaseMessageRef = useRef("");
  const uploadControllersRef = useRef(new Map<string, AbortController>());
  const [attachments, setAttachments] = useState<Attachment[]>([]);
  const [hydratedAttachmentsKey, setHydratedAttachmentsKey] = useState<
    string | null
  >(null);
  const {
    uploadFiles,
    queuedMessages,
    updateQueuedMessage,
    deleteQueuedMessage,
    storageScope,
    terminalPrompt,
  } = useChat();
  const draftStorageKey = `agentapi.chat.message-draft:${storageScope}`;
  const attachmentsStorageKey = `agentapi.chat.attachments:${storageScope}`;

  useEffect(() => {
    const uploadControllers = uploadControllersRef.current;
    const speechWindow = window as typeof window & {
      SpeechRecognition?: SpeechRecognitionConstructor;
      webkitSpeechRecognition?: SpeechRecognitionConstructor;
    };
    setSpeechSupported(
      Boolean(speechWindow.SpeechRecognition || speechWindow.webkitSpeechRecognition),
    );

    return () => {
      recognitionRef.current?.abort();
      uploadControllers.forEach((controller) => controller.abort());
      uploadControllers.clear();
    };
  }, []);

  useEffect(() => {
    try {
      const savedDraft = window.localStorage.getItem(draftStorageKey);
      setMessage(savedDraft ?? "");
    } catch {
      // Storage may be unavailable in privacy-restricted embedded contexts.
    } finally {
      setHydratedDraftKey(draftStorageKey);
    }
  }, [draftStorageKey]);

  useEffect(() => {
    if (hydratedDraftKey !== draftStorageKey) return;
    try {
      if (message) {
        window.localStorage.setItem(draftStorageKey, message);
      } else {
        window.localStorage.removeItem(draftStorageKey);
      }
    } catch {
      // Keep the in-memory draft when persistent storage is unavailable.
    }
  }, [draftStorageKey, hydratedDraftKey, message]);

  useEffect(() => {
    try {
      const restored = parsePersistedAttachments(
        window.localStorage.getItem(attachmentsStorageKey),
      );
      setAttachments(
        restored.map((attachment) => ({
          ...attachment,
          progress: 100,
          status: "completed",
        })),
      );
    } finally {
      setHydratedAttachmentsKey(attachmentsStorageKey);
    }
  }, [attachmentsStorageKey]);

  useEffect(() => {
    if (hydratedAttachmentsKey !== attachmentsStorageKey) return;
    const completed = attachments
      .filter(
        (
          attachment,
        ): attachment is Attachment & {filePath: string} =>
          attachment.status === "completed" &&
          typeof attachment.filePath === "string",
      )
      .map(({id, name, size, filePath}) => ({id, name, size, filePath}));
    try {
      if (completed.length > 0) {
        window.localStorage.setItem(
          attachmentsStorageKey,
          JSON.stringify(completed),
        );
      } else {
        window.localStorage.removeItem(attachmentsStorageKey);
      }
    } catch {
      // Attachments remain available for the current page session.
    }
  }, [attachments, attachmentsStorageKey, hydratedAttachmentsKey]);

  useEffect(() => {
    if (serverStatus !== "running") setIsStopping(false);
  }, [serverStatus]);

  useEffect(() => {
    if (!suggestedPrompt) return;
    setMessage(suggestedPrompt);
    onSuggestedPromptApplied?.();
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  }, [onSuggestedPromptApplied, suggestedPrompt]);

  const startEditingQueuedMessage = (id: number, content: string) => {
    setEditingQueuedID(id);
    setEditingQueuedMessage(content);
  };

  const cancelEditingQueuedMessage = () => {
    setEditingQueuedID(null);
    setEditingQueuedMessage("");
  };

  useEffect(() => {
    if (
      editingQueuedID !== null &&
      !queuedMessages.some((queuedMessage) => queuedMessage.id === editingQueuedID)
    ) {
      setEditingQueuedID(null);
      setEditingQueuedMessage("");
    }
  }, [editingQueuedID, queuedMessages]);

  const saveEditingQueuedMessage = async () => {
    if (editingQueuedID === null || !editingQueuedMessage.trim()) return;

    try {
      await updateQueuedMessage(editingQueuedID, editingQueuedMessage);
      cancelEditingQueuedMessage();
    } catch (error) {
      toast.error("Failed to update queued task", {
        description: getErrorMessage(error),
      });
    }
  };

  const removeQueuedMessage = async (id: number) => {
    try {
      await deleteQueuedMessage(id);
      if (editingQueuedID === id) cancelEditingQueuedMessage();
    } catch (error) {
      toast.error("Failed to delete queued task", {
        description: getErrorMessage(error),
      });
    }
  };

  const uploadAttachment = async (attachment: Attachment) => {
    if (!attachment.file) return;
    const controller = new AbortController();
    uploadControllersRef.current.set(attachment.id, controller);
    setAttachments((previous) =>
      previous.map((item) =>
        item.id === attachment.id
          ? {...item, status: "uploading", progress: 0, error: undefined}
          : item,
      ),
    );

    const formData = new FormData();
    formData.append("file", attachment.file, attachment.name);
    const response = await uploadFiles(formData, {
      signal: controller.signal,
      onProgress: (progress) =>
        setAttachments((previous) =>
          previous.map((item) =>
            item.id === attachment.id ? {...item, progress} : item,
          ),
        ),
    });
    uploadControllersRef.current.delete(attachment.id);

    if (response.ok && response.filePath) {
      setAttachments((previous) =>
        previous.map((item) =>
          item.id === attachment.id
            ? {
                ...item,
                status: "completed",
                progress: 100,
                filePath: response.filePath,
              }
            : item,
        ),
      );
      setMessage((current) => `${current} @"${response.filePath}"`);
    } else if (!controller.signal.aborted) {
      setAttachments((previous) =>
        previous.map((item) =>
          item.id === attachment.id
            ? {
                ...item,
                status: "failed",
                error: response.error ?? "Upload failed.",
              }
            : item,
        ),
      );
    }
  };

  const handleFilesAdded = async (files: File[]) => {
    const accepted: Attachment[] = [];
    for (const file of files) {
      const attachment: Attachment = {
        id: crypto.randomUUID(),
        name: file.name,
        size: file.size,
        file,
        progress: 0,
        status: file.size > MAX_UPLOAD_SIZE ? "failed" : "uploading",
        error:
          file.size > MAX_UPLOAD_SIZE
            ? "File exceeds the 10 MB upload limit."
            : undefined,
      };
      accepted.push(attachment);
    }
    setAttachments((previous) => [...previous, ...accepted]);
    accepted
      .filter((attachment) => attachment.status === "uploading")
      .forEach((attachment) => void uploadAttachment(attachment));
    textareaRef.current?.focus();
  };

  const removeAttachment = (attachment: Attachment) => {
    uploadControllersRef.current.get(attachment.id)?.abort();
    uploadControllersRef.current.delete(attachment.id);
    setAttachments((previous) =>
      previous.filter((item) => item.id !== attachment.id),
    );
    if (attachment.filePath) {
      setMessage((current) =>
        removeAttachmentToken(current, attachment.filePath!),
      );
    }
  };

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (message.trim() && !disabled) {
      if (serverStatus !== "running" && serverStatus !== "stable") return;
      const result = await onSendMessage(message, "user");
      if (!result.ok) return;
      if (result.queued) toast.success("Task queued");
      setMessage("");
      setAttachments((previous) =>
        previous.filter((attachment) => attachment.status !== "completed"),
      );
    }
  };

  // Autofocus on the message input box on user's turn
  useEffect(() => {
    if (
      serverStatus === "stable" &&
      !disabled &&
      window.matchMedia("(min-width: 640px) and (pointer: fine)").matches &&
      textareaRef.current
    ) {
      textareaRef.current.focus();
    }
  }, [serverStatus, disabled]);

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSubmit(e);
    }
  };

  const handleUploadClick = (e?: MouseEvent<HTMLButtonElement>) => {
    e?.preventDefault();
    fileInputRef.current?.click();
  };

  const handleFileInputChange = async (e: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || []);
    if (files.length > 0) {
      await handleFilesAdded(files);
    }
    e.target.value = '';
  };

  const toggleSpeechInput = () => {
    if (isListening) {
      recognitionRef.current?.stop();
      return;
    }

    const speechWindow = window as typeof window & {
      SpeechRecognition?: SpeechRecognitionConstructor;
      webkitSpeechRecognition?: SpeechRecognitionConstructor;
    };
    const Recognition =
      speechWindow.SpeechRecognition || speechWindow.webkitSpeechRecognition;

    if (!Recognition) {
      toast.error("Voice input is not supported by this browser");
      return;
    }

    const recognition = new Recognition();
    recognition.continuous = true;
    recognition.interimResults = true;
    recognition.lang = navigator.language;
    speechBaseMessageRef.current =
      message && !message.endsWith(" ") ? `${message} ` : message;

    recognition.onresult = (event) => {
      let transcript = "";
      for (let index = 0; index < event.results.length; index += 1) {
        transcript += event.results[index][0].transcript;
      }
      setMessage(`${speechBaseMessageRef.current}${transcript}`);
    };
    recognition.onerror = (event) => {
      if (event.error === "not-allowed") {
        toast.error("Microphone permission was denied.");
      } else if (event.error !== "aborted") {
        toast.error("The browser could not recognize speech.");
      }
      setIsListening(false);
    };
    recognition.onend = () => {
      setIsListening(false);
      recognitionRef.current = null;
      textareaRef.current?.focus();
    };

    recognitionRef.current = recognition;
    try {
      recognition.start();
      setIsListening(true);
    } catch {
      recognitionRef.current = null;
      toast.error("The browser could not recognize speech.");
    }
  };

  const handleStop = () => {
    if (isStopping) return;
    setIsStopping(true);
    onSendMessage("\x1b", "raw");
    toast.info("Stop signal sent");
  };

  return (
    <div className="shrink-0 border-t bg-background/90 backdrop-blur-xl">
      {/* Aligned with the transcript's text column (past the time rail). */}
      <div className="mx-auto w-full max-w-[49rem] px-4 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] pt-2 sm:px-6 sm:pb-3 sm:pt-3">
       <div className="grid grid-cols-[minmax(0,1fr)] gap-2">
        {dock}
        <DragDrop
          onFilesAdded={handleFilesAdded}
          disabled={disabled}
        >
          <input
            ref={fileInputRef}
            type="file"
            multiple={true}
            className={"hidden"}
            onChange={handleFileInputChange}
          />
          <form
            onSubmit={handleSubmit}
            className="overflow-hidden rounded-2xl border bg-card shadow-lg shadow-black/5 transition focus-within:border-ring focus-within:ring-2 focus-within:ring-ring/20"
          >
            <div className="flex flex-col">
              <div className="flex">
                  <TextareaAutosize
                    ref={textareaRef}
                    minRows={1}
                    maxRows={4}
                    value={message}
                    onChange={(e) => setMessage(e.target.value)}
                    onKeyDown={handleKeyDown}
                    onPaste={(event) => {
                      // A pasted screenshot or image is uploaded like an
                      // attachment; pasted text pastes as usual.
                      const files = filesToUploadFromPaste(event.clipboardData);
                      if (files.length === 0) return;
                      event.preventDefault();
                      const now = new Date();
                      void handleFilesAdded(files.map((file) => new File([file], pastedFileName(file.name, file.type, now), {type: file.type})));
                    }}
                    aria-label="Task message"
                    placeholder={
                      terminalPrompt
                        ? "Queue a task (runs after you answer)…"
                        : serverStatus === "running"
                          ? "Queue a follow-up…"
                          : serverStatus === "stable"
                            ? "Describe a task…"
                            : "Reconnecting… Your draft is saved."
                    }
                    className="min-h-14 max-h-32 w-full resize-none overflow-y-auto bg-transparent px-4 py-3 text-sm leading-6 outline-none sm:min-h-16 sm:px-5"
                    disabled={
                      disabled ||
                      (serverStatus !== "stable" && serverStatus !== "running")
                    }
                  />
              </div>

              {attachments.length > 0 && (
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
                      <AttachmentIcon attachment={attachment} baseURL={storageScope} />
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
                            onClick={() => void uploadAttachment(attachment)}
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
                        onClick={() => removeAttachment(attachment)}
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
              )}

              {queuedMessages.length > 0 && (
                <details open className="group border-t bg-muted/15">
                  <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2 text-xs font-medium text-muted-foreground [&::-webkit-details-marker]:hidden">
                    <span className="flex items-center gap-1.5">
                      <Clock3 className="size-3" />
                      Queued tasks · {queuedMessages.length}
                      {terminalPrompt && <span className="text-state-needs">· held until you answer the agent</span>}
                    </span>
                    <span className="text-[10px] group-open:hidden">Show</span>
                    <span className="hidden text-[10px] group-open:inline">Hide</span>
                  </summary>
                  <div className="max-h-40 space-y-1.5 overflow-y-auto border-t px-3 py-2">
                    {queuedMessages.map((queuedMessage, index) => (
                      <div
                        key={queuedMessage.id}
                        className="flex w-full items-center gap-1 rounded-md border bg-background py-1 pl-2.5 pr-1 text-xs"
                      >
                        {editingQueuedID === queuedMessage.id ? (
                          <input
                            autoFocus
                            value={editingQueuedMessage}
                            onChange={(event) =>
                              setEditingQueuedMessage(event.target.value)
                            }
                            onKeyDown={(event) => {
                              if (event.key === "Enter") {
                                event.preventDefault();
                                saveEditingQueuedMessage();
                              } else if (event.key === "Escape") {
                                event.preventDefault();
                                cancelEditingQueuedMessage();
                              }
                            }}
                            className="h-6 min-w-0 flex-1 bg-transparent text-xs outline-none"
                            aria-label={`Edit queued task ${index + 1}`}
                          />
                        ) : (
                          <span className="min-w-0 flex-1 truncate">{queuedMessage.content}</span>
                        )}
                        <Button
                          type="button"
                          size="icon"
                          variant="ghost"
                          className="size-6 shrink-0 text-muted-foreground"
                          onClick={() =>
                            editingQueuedID === queuedMessage.id
                              ? saveEditingQueuedMessage()
                              : startEditingQueuedMessage(
                                  queuedMessage.id,
                                  queuedMessage.content,
                                )
                          }
                          disabled={
                            editingQueuedID === queuedMessage.id &&
                            !editingQueuedMessage.trim()
                          }
                          title={
                            editingQueuedID === queuedMessage.id
                              ? "Save queued task"
                              : "Edit queued task"
                          }
                        >
                          {editingQueuedID === queuedMessage.id
                            ? <Check className="size-3" />
                            : <Pencil className="size-3" />}
                          <span className="sr-only">
                            {editingQueuedID === queuedMessage.id ? "Save" : "Edit"} queued task
                          </span>
                        </Button>
                        <Button
                          type="button"
                          size="icon"
                          variant="ghost"
                          className="size-6 shrink-0 text-muted-foreground"
                          onClick={() => removeQueuedMessage(queuedMessage.id)}
                          title="Remove queued task"
                        >
                          <X className="size-3" />
                          <span className="sr-only">Remove queued task</span>
                        </Button>
                      </div>
                    ))}
                  </div>
                </details>
              )}

              <div className="flex items-center justify-end gap-3 border-t bg-muted/25 px-3 py-2">

                <div className="flex min-w-0 flex-row items-center gap-2">
                    <Tooltip>
                      <TooltipTrigger asChild>
                        <Button
                          type="button"
                          size="icon"
                          variant="ghost"
                          className="size-10 rounded-full text-muted-foreground hover:text-foreground"
                          onClick={(e) => handleUploadClick(e)}
                          disabled={disabled}
                        >
                          <Paperclip />
                          <span className="sr-only">Attach files</span>
                        </Button>
                      </TooltipTrigger>
                      <TooltipContent>Attach files</TooltipContent>
                    </Tooltip>
                    <DropdownMenu>
                      <DropdownMenuTrigger asChild>
                        <Button
                          type="button"
                          size="icon"
                          variant={isListening ? "secondary" : "ghost"}
                          className="size-10 rounded-full text-muted-foreground hover:text-foreground"
                          title="More input options"
                        >
                          <MoreHorizontal />
                          <span className="sr-only">More input options</span>
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="min-w-48">
                        <DropdownMenuItem
                          onSelect={toggleSpeechInput}
                          disabled={disabled || !speechSupported}
                          className="min-h-10"
                        >
                          {isListening ? <MicOff /> : <Mic />}
                          {isListening ? "Stop voice input" : "Start voice input"}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>

                  {(serverStatus === "stable" || serverStatus === "running") && (
                    <Button
                      type="submit"
                      disabled={
                        disabled ||
                        !message.trim() ||
                        attachments.some(
                          (attachment) => attachment.status === "uploading",
                        )
                      }
                      size="icon"
                      className="relative size-10 rounded-full shadow-sm"
                      title={
                        serverStatus === "running"
                          ? "Add task to queue"
                          : "Send task"
                      }
                    >
                      {serverStatus === "running" ? <ListPlus /> : <SendIcon />}
                      {serverStatus === "running" && queuedMessages.length > 0 && (
                        <span
                          aria-hidden="true"
                          className="absolute -right-1 -top-1 grid min-h-5 min-w-5 place-items-center rounded-full border-2 border-background bg-status-warning px-1 text-[10px] font-bold leading-none text-foreground"
                        >
                          {queuedMessages.length}
                        </span>
                      )}
                      <span className="sr-only">
                        {serverStatus === "running" ? "Queue task" : "Send task"}
                      </span>
                    </Button>
                  )}

                  {serverStatus === "running" && (
                    <Button
                      size="icon"
                      type="button"
                      variant="destructive"
                      className="size-10 rounded-full shadow-sm"
                      disabled={disabled || isStopping}
                      onClick={handleStop}
                      title={isStopping ? "Stopping agent" : "Stop agent"}
                    >
                      {isStopping
                        ? <LoaderCircle className="animate-spin" />
                        : <Square />}
                      <span className="sr-only">
                        {isStopping ? "Stopping" : "Stop"}
                      </span>
                    </Button>
                  )}

                </div>

              </div>
            </div>
          </form>
        </DragDrop>

        <p className="hidden text-center text-[11px] text-muted-foreground sm:block">
          Enter to send · Shift+Enter for a new line · Keys for the terminal itself: TTY mode
        </p>
       </div>
      </div>

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
