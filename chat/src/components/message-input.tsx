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
} from "lucide-react";
import {Tabs, TabsList, TabsTrigger} from "./ui/tabs";
import type {ServerStatus} from "./chat-provider";
import TextareaAutosize from "react-textarea-autosize";
import {useChat} from "./chat-provider";
import {DragDrop} from "./drag-drop";
import {toast} from "sonner";
import {getErrorMessage} from "@/lib/error-utils";

interface MessageInputProps {
  onSendMessage: (message: string, type: "user" | "raw") => void;
  disabled?: boolean;
  serverStatus: ServerStatus;
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

export default function MessageInput({
  onSendMessage,
  disabled = false,
  serverStatus,
}: MessageInputProps) {
  const [message, setMessage] = useState("");
  const [inputMode, setInputMode] = useState<"text" | "control">("text");
  const [sentChars, setSentChars] = useState<SentChar[]>([]);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const nextCharId = useRef(0);
  const [controlAreaFocused, setControlAreaFocused] = useState(false);
  const [isStopping, setIsStopping] = useState(false);
  const [isListening, setIsListening] = useState(false);
  const [speechSupported, setSpeechSupported] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const recognitionRef = useRef<SpeechRecognitionLike | null>(null);
  const speechBaseMessageRef = useRef("");
  const {uploadFiles} = useChat();

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
      onSendMessage(message, "user");
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

        if (ctrlMappings[e.key.toLowerCase()]) {
          e.preventDefault();
          addSentChar(`Ctrl+${e.key.toUpperCase()}`);
          onSendMessage(ctrlMappings[e.key.toLowerCase()], "raw");
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

  const handleUploadClick = (e: MouseEvent<HTMLButtonElement>) => {
    e.preventDefault();
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
      className="shrink-0 border-t bg-background/85 backdrop-blur-xl"
    >
      <div className="mx-auto w-full max-w-4xl px-4 pb-4 pt-3 sm:px-6 sm:pb-5">
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
                  <div
                    // eslint-disable-next-line @typescript-eslint/no-explicit-any
                    ref={textareaRef as any}
                    tabIndex={0}
                    // eslint-disable-next-line @typescript-eslint/no-explicit-any
                    onKeyDown={handleKeyDown as any}
                    onFocus={() => setControlAreaFocused(true)}
                    onBlur={() => setControlAreaFocused(false)}
                    className="flex h-24 w-full cursor-text items-center justify-center p-4 text-center text-sm text-muted-foreground outline-none focus:bg-muted/35"
                  >
                    {controlAreaFocused
                      ? "Press any key to send to terminal (arrows, Ctrl+C, Ctrl+R, etc.)"
                      : "Click or focus this area to send keystrokes to terminal"}
                  </div>
                ) : (
                  <TextareaAutosize
                    autoFocus
                    ref={textareaRef}
                    value={message}
                    onChange={(e) => setMessage(e.target.value)}
                    onKeyDown={handleKeyDown}
                    placeholder={
                      serverStatus === "running"
                        ? "Running..."
                        : "Type a message..."
                    }
                    className="min-h-20 max-h-[400px] w-full resize-none bg-transparent px-4 pb-2 pt-4 text-sm leading-6 outline-none sm:px-5"
                    disabled={serverStatus !== "stable"}
                  />
                )}
              </div>

              <div className="flex items-center justify-between gap-3 border-t bg-muted/25 px-3 py-2.5">
                <TabsList className="h-8 bg-muted/70 p-0.5">
                  <TabsTrigger
                    value="text"
                    className="h-7 gap-1.5 px-2.5 text-xs"
                    onClick={() => {
                      textareaRef.current?.focus();
                    }}
                  >
                    <MessageSquareText className="size-3.5" />
                    Chat
                  </TabsTrigger>
                  <TabsTrigger
                    value="control"
                    className="h-7 gap-1.5 px-2.5 text-xs"
                    onClick={() => {
                      textareaRef.current?.focus();
                    }}
                  >
                    <Keyboard className="size-3.5" />
                    Control
                  </TabsTrigger>
                </TabsList>

                <div className="flex min-w-0 flex-row items-center gap-2">
                  {inputMode === "text" && serverStatus !== "running" && (
                    <Button
                      type="button"
                      size="icon"
                      variant={isListening ? "secondary" : "ghost"}
                      className={isListening
                        ? "rounded-full text-destructive"
                        : "rounded-full text-muted-foreground hover:text-foreground"}
                      onClick={toggleSpeechInput}
                      disabled={disabled || !speechSupported}
                      aria-pressed={isListening}
                      title={
                        speechSupported
                          ? isListening
                            ? "Stop voice input"
                            : "Start voice input"
                          : "Voice input is not supported by this browser"
                      }
                    >
                      {isListening ? <MicOff /> : <Mic />}
                      <span className="sr-only">
                        {isListening ? "Stop voice input" : "Start voice input"}
                      </span>
                    </Button>
                  )}
                  {serverStatus !== "running" && <Button
                      type="button"
                      size="icon"
                      variant="ghost"
                      className="rounded-full text-muted-foreground hover:text-foreground"
                      onClick={handleUploadClick}
                      title={"Upload File"}
                  >
                      <Paperclip />
                      <span className="sr-only">Upload</span>
                  </Button>
                  }

                  {inputMode === "text" && serverStatus !== "running" && (
                    <Button
                      type="submit"
                      disabled={disabled || !message.trim()}
                      size="icon"
                      className="rounded-full shadow-sm"
                      title={"Send Message"}
                    >
                      <SendIcon/>
                      <span className="sr-only">Send</span>
                    </Button>
                  )}

                  {inputMode === "text" && serverStatus === "running" && (
                    <Button
                      size="icon"
                      type="button"
                      variant="destructive"
                      className="rounded-full shadow-sm"
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

        <div className="mt-2.5 flex items-center justify-center gap-2 text-center text-[11px] text-muted-foreground">
          {inputMode === "text" ? (
            <>
              <Upload className="size-3" />
              <span>Drop files to attach · Enter to send · Shift+Enter for a new line</span>
            </>
          ) : (
            <>
              <Keyboard className="size-3" />
              <span>Keystrokes are sent directly to the agent terminal</span>
            </>
          )}
        </div>
      </div>
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
