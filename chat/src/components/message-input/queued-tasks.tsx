"use client";

import {useEffect, useState} from "react";
import {ArrowDown, ArrowUp, Check, Clock3, CornerDownRight, Pencil, X} from "lucide-react";
import {toast} from "sonner";
import {Button} from "../ui/button";
import {useChatActions, type QueuedMessage} from "../chat-provider";
import {ApiError, getErrorMessage} from "@/lib/error-utils";
import {isImeComposing} from "@/lib/keys";
import {moveInQueue} from "@/lib/dispatch";

// The tasks waiting for the agent: each can be edited, moved, removed, or
// (steerable) added to the turn the agent works on now. held says what
// they wait for besides the turn.
export function QueuedTasks({
  queuedMessages,
  steerable,
  held,
}: {
  queuedMessages: QueuedMessage[];
  steerable: boolean;
  held: "answer" | "login" | "limit" | null;
}) {
  const {updateQueuedMessage, deleteQueuedMessage, steerQueuedMessage, reorderQueue} = useChatActions();
  const [editingQueuedID, setEditingQueuedID] = useState<number | null>(null);
  const [editingQueuedMessage, setEditingQueuedMessage] = useState("");
  const waitingForAnswer = held === "answer";
  const waitingForLogin = held === "login";

  const startEditingQueuedMessage = (id: number, content: string) => {
    setEditingQueuedID(id);
    setEditingQueuedMessage(content);
  };

  const cancelEditingQueuedMessage = () => {
    setEditingQueuedID(null);
    setEditingQueuedMessage("");
  };

  // A task that went out (or was removed elsewhere) is no longer edited.
  useEffect(() => {
    if (editingQueuedID !== null && !queuedMessages.some((queuedMessage) => queuedMessage.id === editingQueuedID)) {
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
      toast.error("Failed to update queued task", {description: getErrorMessage(error)});
    }
  };

  const removeQueuedMessage = async (id: number) => {
    try {
      await deleteQueuedMessage(id);
      if (editingQueuedID === id) cancelEditingQueuedMessage();
    } catch (error) {
      toast.error("Failed to delete queued task", {description: getErrorMessage(error)});
    }
  };

  const steerQueued = async (id: number) => {
    try {
      await steerQueuedMessage(id);
      toast.success("Added to the current turn");
    } catch (error) {
      toast.error("Failed to steer the agent", {description: getErrorMessage(error)});
    }
  };

  const moveQueued = async (id: number, delta: number) => {
    const ids = moveInQueue(queuedMessages.map((queued) => queued.id), id, delta);
    if (!ids) return;
    try {
      await reorderQueue(ids);
    } catch (error) {
      toast.error("Failed to reorder the queue", {
        description: error instanceof ApiError && error.status === 409
          ? "The queue changed in the meantime; try again."
          : getErrorMessage(error),
      });
    }
  };

  return (
    <details open className="group border-t bg-muted/15">
      <summary className="flex cursor-pointer list-none items-center justify-between px-3 py-2 text-xs font-medium text-muted-foreground [&::-webkit-details-marker]:hidden">
        <span className="flex items-center gap-1.5">
          <Clock3 className="size-3" />
          Queued tasks · {queuedMessages.length}
          {held && (
            <span className="text-state-needs">
              · held until {waitingForLogin ? "the agent is logged in" : waitingForAnswer ? "you answer the agent" : "the usage limit resets"}
            </span>
          )}
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
                  // Enter or Esc while picking an IME candidate
                  // only ends the composition.
                  if (isImeComposing(event)) return;
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
            {steerable && editingQueuedID !== queuedMessage.id && (
              <Button
                type="button"
                size="sm"
                variant="ghost"
                className="h-7 shrink-0 gap-1 px-2 text-[11px] text-state-working sm:h-6"
                onClick={() => void steerQueued(queuedMessage.id)}
                title="Steer now: add this task to the turn the agent is working on"
              >
                <CornerDownRight className="size-3" />
                Steer<span className="hidden sm:inline"> now</span>
              </Button>
            )}
            {queuedMessages.length > 1 && editingQueuedID !== queuedMessage.id && (
              <>
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-7 shrink-0 text-muted-foreground sm:size-6"
                  disabled={index === 0}
                  onClick={() => void moveQueued(queuedMessage.id, -1)}
                  title="Move up: send earlier"
                >
                  <ArrowUp className="size-3" />
                  <span className="sr-only">Move queued task {index + 1} up</span>
                </Button>
                <Button
                  type="button"
                  size="icon"
                  variant="ghost"
                  className="size-7 shrink-0 text-muted-foreground sm:size-6"
                  disabled={index === queuedMessages.length - 1}
                  onClick={() => void moveQueued(queuedMessage.id, 1)}
                  title="Move down: send later"
                >
                  <ArrowDown className="size-3" />
                  <span className="sr-only">Move queued task {index + 1} down</span>
                </Button>
              </>
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
  );
}
