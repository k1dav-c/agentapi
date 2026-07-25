"use client";

import {useState, FormEvent, KeyboardEvent, MouseEvent, useEffect, useRef, ChangeEvent} from "react";
import {Button} from "./ui/button";
import {
  ArrowDownIcon,
  ArrowLeftIcon,
  ArrowRightIcon,
  ArrowUpIcon,
  CornerDownLeftIcon,
  DeleteIcon,
  SendIcon,
  Upload,
  Square,
  Keyboard,
  MessageSquareText,
  Paperclip,
  Mic,
  MicOff,
  LoaderCircle,
  ListPlus,
  Clock3,
  Check,
  Pencil,
  X,
  TriangleAlert,
  MoreHorizontal,
} from "lucide-react";
import {Tabs, TabsList, TabsTrigger} from "./ui/tabs";
import type {ServerStatus} from "./chat-provider";
import TextareaAutosize from "react-textarea-autosize";
import {useChat} from "./chat-provider";
import {DragDrop} from "./drag-drop";
import {toast} from "sonner";
import {getErrorMessage} from "@/lib/error-utils";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "./ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";

interface MessageInputProps {
  onSendMessage: (message: string, type: "user" | "raw") => void;
  disabled?: boolean;
  serverStatus: ServerStatus;
  suggestedPrompt?: string;
  onSuggestedPromptApplied?: () => void;
}

interface SentChar {
  char: string;
  id: number;
  timestamp: number;
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

// List of keys to send as raw input when in control mode

const specialKeys: Record<string, string> = {
  ArrowUp: "\x1b[A", // Escape sequence for up arrow
  ArrowDown: "\x1b[B", // Escape sequence for down arrow
  ArrowRight: "\x1b[C", // Escape sequence for right arrow
  ArrowLeft: "\x1b[D", // Escape sequence for left arrow
  Escape: "\x1b", // Escape key
  Tab: "\t", // Tab key
  Delete: "\x1b[3~", // Delete key
  Home: "\x1b[H", // Home key
  End: "\x1b[F", // End key
  PageUp: "\x1b[5~", // Page Up
  PageDown: "\x1b[6~", // Page Down
  Backspace: "\b", // Backspace key
};

const ctrlMappings: Record<string, string> = {
  c: "\x03", // Ctrl+C (SIGINT)
  d: "\x04", // Ctrl+D (EOF)
  z: "\x1A", // Ctrl+Z (SIGTSTP)
  l: "\x0C", // Ctrl+L (clear screen)
  a: "\x01", // Ctrl+A (beginning of line)
  e: "\x05", // Ctrl+E (end of line)
  w: "\x17", // Ctrl+W (delete word)
  u: "\x15", // Ctrl+U (clear line)
  r: "\x12", // Ctrl+R (reverse history search)
};

const controlShortcuts = [
  {label: "Ctrl+C", display: "Ctrl+C", value: ctrlMappings.c},
  {label: "Ctrl+D", display: "Ctrl+D", value: ctrlMappings.d},
  {label: "Ctrl+Z", display: "Ctrl+Z", value: ctrlMappings.z},
  {label: "Ctrl+L", display: "Ctrl+L", value: ctrlMappings.l},
  {label: "Enter", display: "⏎", value: "\r"},
  {label: "Tab", display: "Tab", value: specialKeys.Tab},
  {label: "Escape", display: "Esc", value: specialKeys.Escape},
  {label: "Arrow up", display: "↑", value: specialKeys.ArrowUp},
  {label: "Arrow down", display: "↓", value: specialKeys.ArrowDown},
] as const;

const highRiskControlValues = new Set([ctrlMappings.d, ctrlMappings.z]);

export default function MessageInput({
  onSendMessage,
  disabled = false,
  serverStatus,
  suggestedPrompt = "",
  onSuggestedPromptApplied,
}: MessageInputProps) {
  const [message, setMessage] = useState("");
  const [editingQueuedIndex, setEditingQueuedIndex] = useState<number | null>(null);
  const [editingQueuedMessage, setEditingQueuedMessage] = useState("");
  const [inputMode, setInputMode] = useState<"text" | "control">("text");
  const [sentChars, setSentChars] = useState<SentChar[]>([]);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const nextCharId = useRef(0);
  const [controlAreaFocused, setControlAreaFocused] = useState(false);
  const [isStopping, setIsStopping] = useState(false);
  const [isListening, setIsListening] = useState(false);
  const [speechSupported, setSpeechSupported] = useState(false);
  const [pendingControl, setPendingControl] = useState<
    (typeof controlShortcuts)[number] | null
  >(null);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const recognitionRef = useRef<SpeechRecognitionLike | null>(null);
  const speechBaseMessageRef = useRef("");
  const {
    uploadFiles,
    queuedMessages,
    updateQueuedMessage,
    deleteQueuedMessage,
  } = useChat();

  useEffect(() => {
    const speechWindow = window as typeof window & {
      SpeechRecognition?: SpeechRecognitionConstructor;
      webkitSpeechRecognition?: SpeechRecognitionConstructor;
    };
    setSpeechSupported(
      Boolean(speechWindow.SpeechRecognition || speechWindow.webkitSpeechRecognition),
    );

    return () => recognitionRef.current?.abort();
  }, []);

  useEffect(() => {
    if (serverStatus !== "running") setIsStopping(false);
  }, [serverStatus]);

  useEffect(() => {
    if (!suggestedPrompt) return;
    setMessage(suggestedPrompt);
    setInputMode("text");
    onSuggestedPromptApplied?.();
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  }, [onSuggestedPromptApplied, suggestedPrompt]);

  const startEditingQueuedMessage = (index: number) => {
    setEditingQueuedIndex(index);
    setEditingQueuedMessage(queuedMessages[index].content);
  };

  const cancelEditingQueuedMessage = () => {
    setEditingQueuedIndex(null);
    setEditingQueuedMessage("");
  };

  const saveEditingQueuedMessage = async () => {
    if (editingQueuedIndex === null || !editingQueuedMessage.trim()) return;

    try {
      await updateQueuedMessage(
        queuedMessages[editingQueuedIndex].id,
        editingQueuedMessage,
      );
      cancelEditingQueuedMessage();
    } catch (error) {
      toast.error("Failed to update queued message", {
        description: getErrorMessage(error),
      });
    }
  };

  const removeQueuedMessage = async (index: number) => {
    try {
      await deleteQueuedMessage(queuedMessages[index].id);
      setEditingQueuedIndex((currentIndex) => {
        if (currentIndex === null) return null;
        if (currentIndex === index) {
          setEditingQueuedMessage("");
          return null;
        }
        return currentIndex > index ? currentIndex - 1 : currentIndex;
      });
    } catch (error) {
      toast.error("Failed to delete queued message", {
        description: getErrorMessage(error),
      });
    }
  };

  const handleFilesAdded = async (files: File[]) => {
    for (const file of files) {
      const arrayBuffer = await file.arrayBuffer();

      try {
        // Create Blob from ArrayBuffer
        const blob = new Blob([arrayBuffer], {type: file.type});

        // Create FormData for upload
        const formData = new FormData();
        formData.append('file', blob, file.name);

        // Upload to agent API
        const response = await uploadFiles(formData);
        if (response.ok) {
          setMessage(oldMessage => oldMessage + ' @"' + response.filePath + '"');
        }
      } catch (error) {
        toast.error("Failed to and upload file:", {
          description: getErrorMessage(error),
        });
      }
    }
    textareaRef.current?.focus();
  };

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    if (message.trim() && !disabled) {
      if (serverStatus === "running") {
        onSendMessage(message, "user");
        toast.success("Added to queue", {
          description: `Position ${queuedMessages.length + 1}`,
        });
      } else if (serverStatus === "stable") {
        onSendMessage(message, "user");
      } else {
        return;
      }
      setMessage("");
    }
  };

  // Remove sent characters after they expire (2 seconds)
  useEffect(() => {
    if (sentChars.length === 0) return;

    const interval = setInterval(() => {
      const now = Date.now();
      setSentChars((chars) =>
        chars.filter((char) => now - char.timestamp < 2000)
      );
    }, 100);

    return () => clearInterval(interval);
  }, [sentChars]);

  // Autofocus on the message input box on user's turn
  useEffect(() => {
    if (
      serverStatus === "stable" &&
      !disabled &&
      inputMode === "text" &&
      window.matchMedia("(min-width: 640px) and (pointer: fine)").matches &&
      textareaRef.current
    ) {
      textareaRef.current.focus();
    }
  }, [serverStatus, disabled, inputMode]);

  const addSentChar = (char: string) => {
    const newChar: SentChar = {
      char,
      id: nextCharId.current++,
      timestamp: Date.now(),
    };
    setSentChars((prev) => [...prev, newChar]);
  };

  const sendControlShortcut = (shortcut: (typeof controlShortcuts)[number]) => {
    if (highRiskControlValues.has(shortcut.value)) {
      setPendingControl(shortcut);
      return;
    }
    addSentChar(shortcut.display);
    onSendMessage(shortcut.value, "raw");
    textareaRef.current?.focus();
  };

  const handleKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    // In control mode, send special keys as raw messages
    if (inputMode === "control" && !disabled) {
      // Check if the pressed key is in our special keys map
      if (specialKeys[e.key]) {
        e.preventDefault();
        addSentChar(e.key);
        onSendMessage(specialKeys[e.key], "raw");
        return;
      }

      // Handle Enter as raw newline when in control mode
      if (e.key === "Enter" && !e.shiftKey) {
        e.preventDefault();
        addSentChar("⏎");
        onSendMessage("\r", "raw");
        return;
      }

      // Handle Ctrl+key combinations
      if (e.ctrlKey) {
        if (ctrlMappings[e.key.toLowerCase()]) {
          e.preventDefault();
          const value = ctrlMappings[e.key.toLowerCase()];
          const shortcut = controlShortcuts.find((item) => item.value === value);
          if (shortcut) {
            sendControlShortcut(shortcut);
          } else {
            addSentChar(`Ctrl+${e.key.toUpperCase()}`);
            onSendMessage(value, "raw");
          }
          return;
        }
      }

      // If it's a printable character (length 1), send it as raw input
      if (e.key.length === 1) {
        e.preventDefault();
        addSentChar(e.key);
        onSendMessage(e.key, "raw");
        return;
      }
    } else if (e.key === "Enter" && !e.shiftKey) {
      // Normal Enter handling for text mode with non-empty message
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
    onSendMessage(specialKeys.Escape, "raw");
    toast.info("Stop signal sent");
  };

  return (
    <Tabs
      value={inputMode}
      onValueChange={(value) => setInputMode(value as "text" | "control")}
      className="shrink-0 border-t bg-background/90 backdrop-blur-xl"
    >
      <div className="mx-auto w-full max-w-5xl px-3 pb-[calc(env(safe-area-inset-bottom)+0.5rem)] pt-2 sm:px-6 sm:pb-3 sm:pt-3">
        <DragDrop
          onFilesAdded={handleFilesAdded}
          disabled={disabled || inputMode === "control"}
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
                {inputMode === "control" && !disabled ? (
                  <div className="flex w-full min-w-0 flex-col">
                    <div
                      className="flex items-start gap-2 border-b border-amber-500/25 bg-amber-500/10 px-3 py-2 text-xs text-amber-800 dark:text-amber-300"
                      role="alert"
                    >
                      <TriangleAlert className="mt-0.5 size-3.5 shrink-0" />
                      <span>
                        Direct terminal control. Every key is sent immediately and may stop the agent or close its session.
                      </span>
                    </div>
                    <div
                      // eslint-disable-next-line @typescript-eslint/no-explicit-any
                      ref={textareaRef as any}
                      tabIndex={0}
                      role="application"
                      aria-label="Direct terminal keyboard input"
                      // eslint-disable-next-line @typescript-eslint/no-explicit-any
                      onKeyDown={handleKeyDown as any}
                      onFocus={() => setControlAreaFocused(true)}
                      onBlur={() => setControlAreaFocused(false)}
                      className="flex h-16 w-full cursor-text items-center justify-center p-4 text-center text-sm text-muted-foreground outline-none focus:bg-amber-500/5"
                    >
                      {controlAreaFocused
                        ? "Press any key to send to terminal (arrows, Ctrl+C, Ctrl+R, etc.)"
                        : "Click or focus this area to send keystrokes to terminal"}
                    </div>
                    <div
                      className="flex gap-1.5 overflow-x-auto border-t bg-muted/20 px-3 py-2"
                      aria-label="Terminal shortcuts"
                    >
                      {controlShortcuts.map((shortcut) => (
                        <Button
                          key={shortcut.label}
                          type="button"
                          size="sm"
                          variant="outline"
                          className="h-7 shrink-0 px-2.5 font-mono text-[11px]"
                          onMouseDown={(event) => event.preventDefault()}
                          onClick={() => sendControlShortcut(shortcut)}
                          title={`Send ${shortcut.label}`}
                        >
                          {shortcut.display}
                          <span className="sr-only">Send {shortcut.label}</span>
                        </Button>
                      ))}
                    </div>
                  </div>
                ) : (
                  <TextareaAutosize
                    ref={textareaRef}
                    minRows={1}
                    maxRows={4}
                    value={message}
                    onChange={(e) => setMessage(e.target.value)}
                    onKeyDown={handleKeyDown}
                    placeholder={
                      serverStatus === "running"
                        ? "Add a follow-up task to the queue…"
                        : serverStatus === "stable"
                          ? "Ask the agent to do something…"
                          : "Reconnecting… Your draft is saved."
                    }
                    className="min-h-14 max-h-32 w-full resize-none overflow-y-auto bg-transparent px-4 py-3 text-sm leading-6 outline-none sm:min-h-16 sm:px-5"
                    disabled={
                      disabled ||
                      (serverStatus !== "stable" && serverStatus !== "running")
                    }
                  />
                )}
              </div>

              {inputMode === "text" && queuedMessages.length > 0 && (
                <details className="group border-t bg-muted/15">
                  <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2 text-xs font-medium text-muted-foreground [&::-webkit-details-marker]:hidden">
                    <span className="flex items-center gap-1.5">
                      <Clock3 className="size-3" />
                      Queued messages · {queuedMessages.length}
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
                        {editingQueuedIndex === index ? (
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
                            className="h-6 w-56 min-w-0 bg-transparent text-xs outline-none"
                            aria-label={`Edit queued message ${index + 1}`}
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
                            editingQueuedIndex === index
                              ? saveEditingQueuedMessage()
                              : startEditingQueuedMessage(index)
                          }
                          disabled={
                            editingQueuedIndex === index &&
                            !editingQueuedMessage.trim()
                          }
                          title={
                            editingQueuedIndex === index
                              ? "Save queued message"
                              : "Edit queued message"
                          }
                        >
                          {editingQueuedIndex === index
                            ? <Check className="size-3" />
                            : <Pencil className="size-3" />}
                          <span className="sr-only">
                            {editingQueuedIndex === index ? "Save" : "Edit"} queued message
                          </span>
                        </Button>
                        <Button
                          type="button"
                          size="icon"
                          variant="ghost"
                          className="size-6 shrink-0 text-muted-foreground"
                          onClick={() => removeQueuedMessage(index)}
                          title="Remove queued message"
                        >
                          <X className="size-3" />
                          <span className="sr-only">Remove queued message</span>
                        </Button>
                      </div>
                    ))}
                  </div>
                </details>
              )}

              <div className="flex items-center justify-between gap-3 border-t bg-muted/25 px-3 py-2.5">
                <TabsList className="h-10 bg-muted/70 p-0.5 sm:h-8">
                  <TabsTrigger
                    value="text"
                    className="h-9 gap-1.5 px-3 text-xs sm:h-7 sm:px-2.5"
                    onClick={() => {
                      textareaRef.current?.focus();
                    }}
                  >
                    <MessageSquareText className="size-3.5" />
                    Task
                  </TabsTrigger>
                  <TabsTrigger
                    value="control"
                    className="h-9 gap-1.5 px-3 text-xs data-[state=active]:text-amber-700 dark:data-[state=active]:text-amber-300 sm:h-7 sm:px-2.5"
                    onClick={() => {
                      textareaRef.current?.focus();
                    }}
                  >
                    <Keyboard className="size-3.5" />
                    Terminal
                  </TabsTrigger>
                </TabsList>

                <div className="flex min-w-0 flex-row items-center gap-2">
                  {inputMode === "text" && (
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
                          onSelect={() => handleUploadClick()}
                          disabled={disabled || serverStatus === "running"}
                          className="min-h-10"
                        >
                          <Paperclip />
                          Attach files
                        </DropdownMenuItem>
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
                  )}

                  {inputMode === "text" &&
                    (serverStatus === "stable" || serverStatus === "running") && (
                    <Button
                      type="submit"
                      disabled={disabled || !message.trim()}
                      size="icon"
                      className="relative size-10 rounded-full shadow-sm"
                      title={
                        serverStatus === "running"
                          ? "Add message to queue"
                          : "Send message"
                      }
                    >
                      {serverStatus === "running" ? <ListPlus /> : <SendIcon />}
                      {serverStatus === "running" && queuedMessages.length > 0 && (
                        <span
                          aria-hidden="true"
                          className="absolute -right-1 -top-1 grid min-h-5 min-w-5 place-items-center rounded-full border-2 border-background bg-amber-500 px-1 text-[10px] font-bold leading-none text-white"
                        >
                          {queuedMessages.length}
                        </span>
                      )}
                      <span className="sr-only">
                        {serverStatus === "running" ? "Queue task" : "Send task"}
                      </span>
                    </Button>
                  )}

                  {inputMode === "text" && serverStatus === "running" && (
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

                  {inputMode === "control" && !disabled && (
                    <div className="flex items-center gap-1">
                      {sentChars.map((char) => (
                        <span
                          key={char.id}
                          className="flex h-8 min-w-8 animate-pulse items-center justify-center rounded-md border bg-background px-2 font-mono text-xs font-medium"
                        >
                      <Char char={char.char}/>
                    </span>
                      ))}
                    </div>
                  )}
                </div>

              </div>
            </div>
          </form>
        </DragDrop>

        <div className="mt-2 hidden items-center justify-center gap-2 text-center text-[11px] text-muted-foreground sm:flex">
            {inputMode === "text" ? (
              <>
                <Upload className="size-3" />
                <span>Enter to send · Shift+Enter for a new line · More options for files and voice</span>
              </>
            ) : (
              <>
                <Keyboard className="size-3" />
                <span>Advanced mode: keystrokes are sent directly to the agent terminal</span>
              </>
            )}
        </div>
      </div>

      <Dialog open={pendingControl !== null} onOpenChange={(open) => !open && setPendingControl(null)}>
        <DialogContent className="w-[calc(100%-2rem)] rounded-xl sm:max-w-md">
          <DialogHeader>
            <DialogTitle>Send {pendingControl?.display} to the terminal?</DialogTitle>
            <DialogDescription>
              This shortcut can suspend the process or close the current agent session. It takes effect immediately.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter className="gap-2">
            <Button type="button" variant="outline" onClick={() => setPendingControl(null)}>
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={() => {
                if (!pendingControl) return;
                addSentChar(pendingControl.display);
                onSendMessage(pendingControl.value, "raw");
                setPendingControl(null);
              }}
            >
              Send shortcut
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Tabs>
  );
}

function Char({char}: { char: string }) {
  switch (char) {
    case "ArrowUp":
      return <ArrowUpIcon className="h-4 w-4"/>;
    case "ArrowDown":
      return <ArrowDownIcon className="h-4 w-4"/>;
    case "ArrowRight":
      return <ArrowRightIcon className="h-4 w-4"/>;
    case "ArrowLeft":
      return <ArrowLeftIcon className="h-4 w-4"/>;
    case "⏎":
      return <CornerDownLeftIcon className="h-4 w-4"/>;
    case "Backspace":
      return <DeleteIcon className="h-4 w-4"/>;
    default:
      return char;
  }
}
