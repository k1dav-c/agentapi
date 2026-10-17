"use client";

import { useMemo, useState } from "react";
import TextareaAutosize from "react-textarea-autosize";
import { ClipboardList, MessageCircleQuestion, ShieldQuestion, X } from "lucide-react";
import { toast } from "sonner";
import { useChat } from "./chat-provider";
import { CardStatus, CommandBlock, DecisionCardShell, MarkdownDetail, TONE_CLASS, optionRowClass } from "./decision-card";
import { QuestionSteps } from "./question-steps";
import { Kbd } from "./ui/kbd";
import { getToolCommand } from "@/lib/tool-format";
import { ApiError } from "@/lib/error-utils";
import { isImeComposing } from "@/lib/keys";
import { useNumberKeys } from "@/lib/use-number-keys";
import {
  answerByMessageHint,
  optionResponse,
  optionTakesFeedback,
  optionTone,
  pendingRequests,
  respondErrorMessage,
  type NativeRequest,
  type RequestOption,
  type RequestResponse,
} from "@/lib/native-requests";

const KIND_META = {
  approval: { label: "Approval", icon: ShieldQuestion },
  question: { label: "Question", icon: MessageCircleQuestion },
  plan: { label: "Plan", icon: ClipboardList },
} as const;

// The request the agent waits on (native transport), docked above the
// composer like the terminal prompt card. One request shows at a time,
// oldest first.
export function RequestCard() {
  const { requests, messages } = useChat();
  const pending = useMemo(() => pendingRequests(requests), [requests]);
  const [dismissedHint, setDismissedHint] = useState("");
  const request = pending[0];
  if (request) return <RequestCardBody key={request.id} request={request} total={pending.length} />;

  const lastUserTime = messages.findLast((message) => message.role === "user")?.time;
  const hint = answerByMessageHint(requests, lastUserTime);
  if (!hint || hint.id === dismissedHint) return null;
  return (
    <div className="flex items-start gap-2 rounded-xl border border-dashed px-3 py-2 text-xs text-muted-foreground" role="status">
      <MessageCircleQuestion className="mt-0.5 size-3.5 shrink-0 text-state-needs" />
      <p className="min-w-0 flex-1">
        The agent stopped waiting for an answer to <span className="font-medium text-foreground">{hint.title}</span>.
        You can still answer it in a normal message.
      </p>
      <button
        type="button"
        onClick={() => setDismissedHint(hint.id)}
        className="grid size-6 shrink-0 place-items-center rounded outline-none transition hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        aria-label="Dismiss"
      >
        <X className="size-3.5" />
      </button>
    </div>
  );
}

function RequestCardBody({ request, total }: { request: NativeRequest; total: number }) {
  const { respondToRequest } = useChat();
  const [sending, setSending] = useState(false);
  const [error, setError] = useState("");
  const meta = KIND_META[request.kind] ?? KIND_META.approval;

  const respond = async (response: RequestResponse) => {
    if (sending) return;
    setSending(true);
    setError("");
    try {
      await respondToRequest(request.id, response);
    } catch (failure) {
      const message = respondErrorMessage(failure);
      setError(message);
      if (failure instanceof ApiError && (failure.status === 409 || failure.status === 404)) {
        toast.info(message);
      }
      setSending(false);
    }
  };

  return (
    <DecisionCardShell
      data-request-kind={request.kind}
      icon={meta.icon}
      label={meta.label}
      meta={
        <>
          {request.tool_name && <span className="normal-case tracking-normal text-muted-foreground">· {request.tool_name}</span>}
          {total > 1 && <span className="ml-auto normal-case tracking-normal text-muted-foreground">1 of {total} waiting</span>}
        </>
      }
      title={request.title}
    >
      {request.kind === "question" ? (
        <QuestionSteps request={request} sending={sending} onSubmit={respond} />
      ) : (
        <OptionsBody request={request} sending={sending} onRespond={respond} />
      )}
      <CardStatus className="px-3.5 pb-3 text-[11px]" status={sending ? "Sending your answer…" : ""} error={error} />
    </DecisionCardShell>
  );
}

// An approval or a plan: the context, the options and optional feedback.
function OptionsBody({
  request,
  sending,
  onRespond,
}: {
  request: NativeRequest;
  sending: boolean;
  onRespond: (response: RequestResponse) => void;
}) {
  const [feedback, setFeedback] = useState("");
  const options = request.options ?? [];
  // The command, unless the title already is the command.
  const toolCommand = request.kind === "approval" ? getToolCommand(request.tool_name ?? "", request.input) : undefined;
  const command = toolCommand && !request.title.includes(toolCommand) ? toolCommand : undefined;
  const feedbackOption = options.find((option) => optionTakesFeedback(request, option));
  const choose = (option: RequestOption) => onRespond(optionResponse(request, option, feedback));
  useNumberKeys(options.length, (index) => choose(options[index]));

  return (
    <div className="grid gap-2 px-3.5 pb-2 pt-1">
      {command && <CommandBlock command={command} />}
      {request.detail && <MarkdownDetail content={request.detail} tall={request.kind === "plan"} data-request-detail />}
      <div className={`grid gap-1.5 ${options.length <= 3 ? "sm:grid-flow-col sm:auto-cols-fr" : ""}`}>
        {options.map((option, index) => {
          const tone = optionTone(option.kind);
          const withFeedback = optionTakesFeedback(request, option) && feedback.trim() !== "";
          return (
            <button
              key={option.id}
              type="button"
              disabled={sending}
              onClick={() => choose(option)}
              title={option.description}
              data-option-kind={option.kind}
              className={`${optionRowClass} ${TONE_CLASS[tone]}`}
            >
              <Kbd inverted={tone === "primary"}>{index + 1}</Kbd>
              <span className="min-w-0 flex-1">
                <span className="block font-medium">{withFeedback ? `${option.label} with feedback` : option.label}</span>
                {option.description && (
                  // Clamped: a permission rule can list long paths, which
                  // stretched every button of the row. The title shows it all.
                  <span className={`line-clamp-2 text-[11.5px] [overflow-wrap:anywhere] ${tone === "primary" ? "text-primary-foreground/75" : "text-muted-foreground"}`}>
                    {option.description}
                  </span>
                )}
              </span>
            </button>
          );
        })}
      </div>
      {feedbackOption && (
        <label className="grid gap-1">
          <span className="sr-only">Feedback for the agent</span>
          <TextareaAutosize
            minRows={1}
            maxRows={4}
            value={feedback}
            disabled={sending}
            onChange={(event) => setFeedback(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter" && !event.shiftKey && !isImeComposing(event) && feedback.trim()) {
                event.preventDefault();
                choose(feedbackOption);
              }
            }}
            placeholder={
              request.kind === "plan"
                ? `What should change? Sent with “${feedbackOption.label}”`
                : `Optional: what to do instead. Sent with “${feedbackOption.label}”`
            }
            className="w-full resize-none rounded-lg border bg-background px-2.5 py-2 text-[13px] leading-5 outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/20"
          />
        </label>
      )}
    </div>
  );
}
