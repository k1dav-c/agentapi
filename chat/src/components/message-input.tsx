"use client";

import {useState, FormEvent, KeyboardEvent, MouseEvent, useEffect, useRef, ChangeEvent} from "react";
import {Button} from "./ui/button";
import {Tooltip, TooltipTrigger, TooltipContent} from "./ui/tooltip";
import {Square, Paperclip, Mic, MicOff, LoaderCircle, MoreHorizontal} from "lucide-react";
import type {SendResult} from "./chat-provider";
import type {ServerStatus} from "@/lib/agent-status";
import TextareaAutosize from "react-textarea-autosize";
import {useChat} from "./chat-provider";
import {SessionSettings} from "./session-settings";
import {useSlashCommandRunner} from "./slash-command-runner";
import {completion, matchingCommands, parseSlashCommand, runsHere} from "@/lib/slash-commands";
import {UsageMeter} from "./usage-meter";
import {DragDrop} from "./drag-drop";
import {filesToUploadFromPaste, pastedFileName} from "@/lib/uploads";
import {toast} from "sonner";
import {usePersisted} from "@/lib/use-persisted";
import {DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger} from "./ui/dropdown-menu";
import {canSteer, dispatchModeForKey, dispatchModes, sendNotice, turnActive, type DispatchMode} from "@/lib/dispatch";
import {AttachmentList, useAttachments} from "./message-input/attachments";
import {QueuedTasks} from "./message-input/queued-tasks";
import {SendButtonGroup} from "./message-input/send-button";
import {useSpeechInput} from "./message-input/speech";

interface MessageInputProps {
  onSendMessage: (message: string, type: "user" | "raw", mode?: DispatchMode) => Promise<SendResult>;
  disabled?: boolean;
  serverStatus: ServerStatus;
  suggestedPrompt?: string;
  onSuggestedPromptApplied?: () => void;
  // Shown above the composer: the agent's state strip and decision card.
  dock?: React.ReactNode;
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
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const [isStopping, setIsStopping] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);
  const {
    uploadFiles,
    queuedMessages,
    storageScope,
    features,
    waitingFor,
    dispatch,
    canStop,
    stop,
  } = useChat();
  const modes = dispatchModes(dispatch);
  const steerable = canSteer(dispatch);
  const active = turnActive(dispatch);
  // Nothing queued runs until the agent has its answer, or its login.
  const waitingForLogin = waitingFor === "login";
  const waitingForAnswer = waitingFor === "answer";
  // Shortcut labels: ⌘ on Apple devices, Ctrl elsewhere.
  const [modKey, setModKey] = useState("Ctrl");
  useEffect(() => {
    if (/Mac|iPhone|iPad/.test(navigator.platform)) setModKey("⌘");
  }, []);
  const focusComposer = () => textareaRef.current?.focus();

  // The draft, and the files attached to it, survive a reload.
  usePersisted(
    `agentapi.chat.message-draft:${storageScope}`,
    message,
    (draft) => draft || null,
    (stored) => setMessage(stored ?? ""),
  );
  const attachments = useAttachments({storageKey: `agentapi.chat.attachments:${storageScope}`, uploadFiles, setMessage});
  const speech = useSpeechInput(message, setMessage, focusComposer);

  useEffect(() => {
    if (!active) setIsStopping(false);
  }, [active]);

  useEffect(() => {
    if (!suggestedPrompt) return;
    setMessage(suggestedPrompt);
    onSuggestedPromptApplied?.();
    window.requestAnimationFrame(() => textareaRef.current?.focus());
  }, [onSuggestedPromptApplied, suggestedPrompt]);

  const handleFilesAdded = (files: File[]) => {
    attachments.add(files);
    focusComposer();
  };

  // Not while an attachment uploads: its path joins the message once the
  // upload is done.
  const sendBlocked = disabled || !message.trim() || attachments.uploading;

  const slashCommands = useSlashCommandRunner();
  // A slash command that runs: a second Enter doesn't run it again.
  const commandRunning = useRef(false);
  // The chat's own slash commands, offered while the command word is typed
  // (native agents, which have no terminal UI for them).
  const slashSuggestions = features.slashCommands ? matchingCommands(message).filter((command) => runsHere(command, features)) : [];
  const completeSlashCommand = (text: string) => {
    setMessage(text);
    textareaRef.current?.focus();
  };

  const submit = async (mode: DispatchMode = "queue") => {
    if (sendBlocked || (serverStatus !== "running" && serverStatus !== "stable")) return;
    const command = features.slashCommands ? parseSlashCommand(message) : null;
    if (command && slashCommands.handles(command)) {
      if (commandRunning.current) return;
      commandRunning.current = true;
      // Cleared at once: the next message can be typed while it runs.
      setMessage("");
      try {
        await slashCommands.run(command);
      } finally {
        commandRunning.current = false;
      }
      return;
    }
    const result = await onSendMessage(message, "user", mode);
    if (!result.ok) return;
    const notice = sendNotice(result, mode);
    if (notice) toast.success(notice);
    setMessage("");
    attachments.clearSent();
  };

  const handleSubmit = (e: FormEvent) => {
    e.preventDefault();
    void submit("queue");
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
    // Tab completes a command, unless it is complete: then Tab leaves the
    // composer as usual.
    const completed = slashSuggestions.length > 0 ? completion(slashSuggestions[0]) : "";
    if (e.key === "Tab" && !e.shiftKey && completed && completed !== message) {
      e.preventDefault();
      completeSlashCommand(completed);
      return;
    }
    const mode = dispatchModeForKey(e, modes);
    if (mode) {
      e.preventDefault();
      void submit(mode);
    }
  };

  const handleUploadClick = (e?: MouseEvent<HTMLButtonElement>) => {
    e?.preventDefault();
    fileInputRef.current?.click();
  };

  const handleFileInputChange = (e: ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(e.target.files || []);
    if (files.length > 0) handleFilesAdded(files);
    e.target.value = '';
  };

  const handleStop = () => {
    if (isStopping) return;
    setIsStopping(true);
    void stop().then((stopped) => {
      if (!stopped) setIsStopping(false);
    });
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
              {slashSuggestions.length > 0 && (
                <div className="border-b bg-muted/30 px-2 py-1.5" role="group" aria-label="Chat commands" data-slash-suggestions>
                  {slashSuggestions.map((command) => (
                    <button
                      key={command.name}
                      type="button"
                      onMouseDown={(event) => event.preventDefault()}
                      onClick={() => completeSlashCommand(completion(command))}
                      className="flex w-full items-baseline gap-2 rounded-md px-2 py-1 text-left text-xs hover:bg-muted"
                    >
                      <code className="shrink-0 font-mono text-foreground">/{command.name}</code>
                      {command.args && <span className="shrink-0 font-mono text-muted-foreground max-sm:hidden">{command.args}</span>}
                      <span className="min-w-0 truncate text-muted-foreground">{command.description}</span>
                    </button>
                  ))}
                  <p className="px-2 pt-0.5 text-[11px] text-muted-foreground">Tab completes; other commands go to the agent.</p>
                </div>
              )}
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
                      handleFilesAdded(files.map((file) => new File([file], pastedFileName(file.name, file.type, now), {type: file.type})));
                    }}
                    aria-label="Task message"
                    placeholder={
                      waitingForLogin
                        ? "Queue a task (runs after the agent is logged in)…"
                        : waitingForAnswer
                          ? "Queue a task (runs after you answer)…"
                          : serverStatus === "running"
                            ? steerable ? "Queue a follow-up, or steer the agent…" : "Queue a follow-up…"
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

              {attachments.attachments.length > 0 && (
                <AttachmentList
                  attachments={attachments.attachments}
                  baseURL={storageScope}
                  onRetry={attachments.retry}
                  onRemove={attachments.remove}
                />
              )}

              {queuedMessages.length > 0 && (
                <QueuedTasks
                  queuedMessages={queuedMessages}
                  steerable={steerable}
                  held={waitingForLogin ? "login" : waitingForAnswer ? "answer" : null}
                />
              )}

              <div className="flex flex-wrap items-center justify-end gap-x-3 gap-y-1 border-t bg-muted/25 px-3 py-2">

                {/* The agent's settings and usage: a row of their own on a
                    phone, where they don't fit beside the buttons. */}
                <div className="mr-auto flex min-w-0 items-center gap-1 empty:hidden max-sm:w-full">
                  <SessionSettings />
                  <UsageMeter />
                </div>

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
                          variant={speech.listening ? "secondary" : "ghost"}
                          className="size-10 rounded-full text-muted-foreground hover:text-foreground"
                          title="More input options"
                        >
                          <MoreHorizontal />
                          <span className="sr-only">More input options</span>
                        </Button>
                      </DropdownMenuTrigger>
                      <DropdownMenuContent align="end" className="min-w-48">
                        <DropdownMenuItem
                          onSelect={speech.toggle}
                          disabled={disabled || !speech.supported}
                          className="min-h-10"
                        >
                          {speech.listening ? <MicOff /> : <Mic />}
                          {speech.listening ? "Stop voice input" : "Start voice input"}
                        </DropdownMenuItem>
                      </DropdownMenuContent>
                    </DropdownMenu>

                  {(serverStatus === "stable" || serverStatus === "running") && (
                    <SendButtonGroup
                      serverStatus={serverStatus}
                      modes={modes}
                      sendBlocked={sendBlocked}
                      queuedCount={queuedMessages.length}
                      modKey={modKey}
                      onSubmit={(mode) => void submit(mode)}
                    />
                  )}

                  {canStop && (
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
          {features.agentTerminal
            ? "Enter to send · Shift+Enter for a new line · Keys for the terminal itself: TTY mode"
            : [
                "Enter to send",
                features.steer ? `${modKey}+Enter to steer while it works` : "",
                features.interrupt ? `${modKey}+Shift+Enter to interrupt & send` : "",
                "Shift+Enter for a new line",
              ].filter(Boolean).join(" · ")}
        </p>
       </div>
      </div>

    </div>
  );
}
