"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import TextareaAutosize from "react-textarea-autosize";
import { Check, ClipboardList, MessageCircleQuestion, ShieldQuestion, X } from "lucide-react";
import { toast } from "sonner";
import { useChat } from "./chat-provider";
import { ProcessedMessage } from "./processed-message";
import { getToolCommand } from "@/lib/tool-format";
import {
  acceptsText,
  answerByMessageHint,
  buildQuestionResponse,
  emptyDrafts,
  optionResponse,
  optionTakesFeedback,
  optionTone,
  pendingRequests,
  questionError,
  RespondError,
  respondErrorMessage,
  toggleOption,
  type NativeRequest,
  type QuestionDraft,
  type RequestOption,
  type RequestResponse,
} from "@/lib/native-requests";

const KIND_META = {
  approval: { label: "Approval", icon: ShieldQuestion },
  question: { label: "Question", icon: MessageCircleQuestion },
  plan: { label: "Plan", icon: ClipboardList },
} as const;

const TONE_CLASS = {
  primary: "border-primary bg-primary text-primary-foreground hover:bg-primary/90",
  secondary: "border-transparent bg-secondary text-secondary-foreground hover:bg-secondary/70",
  destructive: "border-destructive/45 text-destructive hover:border-destructive/70 hover:bg-destructive/10",
  neutral: "hover:border-state-needs/60 hover:bg-state-needs/10",
} as const;

const optionRowClass =
  "flex min-h-10 w-full items-center gap-3 rounded-lg border px-2.5 py-1.5 text-left text-[13px] outline-none transition focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60";

function Kbd({ children, inverted = false }: { children: React.ReactNode; inverted?: boolean }) {
  return (
    <kbd
      className={`grid min-w-5 shrink-0 place-items-center rounded border border-b-2 px-1 font-mono text-[10.5px] ${
        inverted ? "border-primary-foreground/30 bg-primary-foreground/10 text-primary-foreground" : "bg-background text-muted-foreground"
      }`}
    >
      {children}
    </kbd>
  );
}

// Number keys answer the card, as on the terminal prompt card, unless the
// focus is in a text field.
function useNumberKeys(count: number, onKey: (index: number) => void) {
  const handler = useRef(onKey);
  handler.current = onKey;
  useEffect(() => {
    if (count === 0) return;
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.metaKey || event.ctrlKey || event.altKey) return;
      const target = event.target as HTMLElement | null;
      if (target && (target.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName))) return;
      const index = Number(event.key) - 1;
      if (Number.isInteger(index) && index >= 0 && index < Math.min(count, 9)) {
        event.preventDefault();
        handler.current(index);
      }
    };
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [count]);
}

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
  const Icon = meta.icon;

  const respond = async (response: RequestResponse) => {
    if (sending) return;
    setSending(true);
    setError("");
    try {
      await respondToRequest(request.id, response);
    } catch (failure) {
      const message = respondErrorMessage(failure);
      setError(message);
      if (failure instanceof RespondError && (failure.status === 409 || failure.status === 404)) {
        toast.info(message);
      }
      setSending(false);
    }
  };

  return (
    <section
      aria-label="The agent is asking"
      data-request-kind={request.kind}
      className="decision-card overflow-hidden rounded-xl border border-state-needs/45 bg-card shadow-lg shadow-black/5"
    >
      <header className="px-3.5 pb-1 pt-3">
        <div className="mb-1 flex items-center gap-1.5 font-mono text-[10.5px] uppercase tracking-[0.06em] text-state-needs">
          <Icon className="size-3.5" />
          <span>{meta.label}</span>
          {request.tool_name && <span className="normal-case tracking-normal text-muted-foreground">· {request.tool_name}</span>}
          {total > 1 && <span className="ml-auto normal-case tracking-normal text-muted-foreground">1 of {total} waiting</span>}
        </div>
        <h2 className="min-w-0 text-sm font-semibold leading-5 [overflow-wrap:anywhere]">{request.title}</h2>
      </header>
      {request.kind === "question" ? (
        <QuestionSteps request={request} sending={sending} onSubmit={respond} />
      ) : (
        <OptionsBody request={request} sending={sending} onRespond={respond} />
      )}
      <p className="px-3.5 pb-3 text-[11px] empty:hidden" role={error ? "alert" : "status"}>
        {error ? <span className="text-destructive">{error}</span> : sending ? <span className="text-muted-foreground">Sending your answer…</span> : null}
      </p>
    </section>
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
      {command && (
        <pre className="max-h-28 overflow-auto whitespace-pre-wrap break-words rounded-md bg-term px-3 py-2 font-mono text-xs leading-[1.55] text-term-foreground [overflow-wrap:anywhere]">
          <span className="mr-2 text-term-foreground/60" aria-hidden="true">$</span>
          {command}
        </pre>
      )}
      {request.detail && (
        <div
          className={`overflow-auto rounded-md border bg-background px-3 py-2 [&_.prose-transcript]:text-[13.5px] [&_.prose-transcript]:leading-[1.6] [&_h1]:my-2 [&_h1]:text-[15px] [&_h1]:font-semibold [&_h2]:my-2 [&_h2]:text-[14.5px] [&_h2]:font-semibold [&_h3]:my-1.5 [&_h3]:font-semibold [&_:is(h1,h2,h3):first-child]:mt-0 ${
            request.kind === "plan" ? "max-h-[min(22rem,42dvh)]" : "max-h-32"
          }`}
          data-request-detail
        >
          <ProcessedMessage messageContent={request.detail} isUser={false} renderMode="markdown" />
        </div>
      )}
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
                  <span className={`block text-[11.5px] ${tone === "primary" ? "text-primary-foreground/75" : "text-muted-foreground"}`}>
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
              if (event.key === "Enter" && !event.shiftKey && feedback.trim()) {
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

// A question request: one step per question, then all answers at once.
function QuestionSteps({
  request,
  sending,
  onSubmit,
}: {
  request: NativeRequest;
  sending: boolean;
  onSubmit: (response: RequestResponse) => void;
}) {
  const questions = request.questions ?? [];
  const [drafts, setDrafts] = useState<Record<string, QuestionDraft>>(() => emptyDrafts(request));
  const [step, setStep] = useState(0);
  const [stepError, setStepError] = useState("");
  const textRef = useRef<HTMLInputElement>(null);
  const question = questions[step];
  const draft = question ? drafts[question.id] ?? { selected: [], text: "" } : undefined;
  const last = step === questions.length - 1;
  const options = question?.options ?? [];

  const update = (next: QuestionDraft) => {
    if (!question) return;
    setDrafts((current) => ({ ...current, [question.id]: next }));
    setStepError("");
  };

  const goTo = (index: number) => {
    setStep(index);
    setStepError("");
  };

  const next = () => {
    if (!question || !draft) return;
    const error = questionError(question, draft);
    if (error) {
      setStepError(error);
      return;
    }
    if (!last) {
      goTo(step + 1);
      return;
    }
    const built = buildQuestionResponse(request, drafts);
    if ("error" in built) {
      setStep(built.question);
      setStepError(built.error);
      return;
    }
    onSubmit(built.response);
  };

  const pick = (label: string) => {
    if (!question || !draft || sending) return;
    const updated = toggleOption(question, draft, label);
    update(updated);
    // A single choice answers the question: move on, but stay on the last
    // one so the answers are submitted on purpose.
    if (!question.multi_select && !last) goTo(step + 1);
  };

  useNumberKeys(options.length, (index) => pick(options[index].label));

  // A question without options is answered by typing; focus its field,
  // except on touch screens where that would open the keyboard unasked.
  useEffect(() => {
    if (question && !options.length && window.matchMedia("(pointer: fine)").matches) {
      textRef.current?.focus({ preventScroll: true });
    }
  }, [question, options.length]);

  if (!question || !draft) return null;
  const answered = (index: number) => questionError(questions[index], drafts[questions[index].id]) === null;

  return (
    <div className="grid gap-2 px-3.5 pb-2 pt-1">
      {questions.length > 1 && (
        <nav aria-label="Questions" className="flex flex-wrap gap-1">
          {questions.map((item, index) => (
            <button
              key={item.id}
              type="button"
              onClick={() => goTo(index)}
              aria-current={index === step ? "step" : undefined}
              className={`flex h-7 items-center gap-1 rounded-full border px-2.5 text-[11.5px] outline-none transition focus-visible:ring-2 focus-visible:ring-ring ${
                index === step ? "border-state-needs/60 bg-state-needs/10 font-medium text-foreground" : "text-muted-foreground hover:text-foreground"
              }`}
            >
              {answered(index) && <Check className="size-3 text-state-ready" strokeWidth={3} />}
              {item.header || `Question ${index + 1}`}
            </button>
          ))}
        </nav>
      )}
      <p className="text-[13px] font-medium leading-5">
        {questions.length > 1 && <span className="mr-1.5 font-mono text-[11px] text-muted-foreground">{step + 1}/{questions.length}</span>}
        {question.question}
      </p>
      {options.length > 0 && (
        <div className="grid gap-1.5" role={question.multi_select ? "group" : "radiogroup"} aria-label={question.question}>
          {options.map((option, index) => {
            const checked = draft.selected.includes(option.label) && !(draft.text.trim() && !question.multi_select);
            return (
              <button
                key={`${option.label}-${index}`}
                type="button"
                role={question.multi_select ? "checkbox" : "radio"}
                aria-checked={checked}
                disabled={sending}
                onClick={() => pick(option.label)}
                className={`${optionRowClass} ${checked ? "border-state-needs/70 bg-state-needs/10" : TONE_CLASS.neutral}`}
              >
                <Kbd>{index + 1}</Kbd>
                <span className="min-w-0 flex-1">
                  <span className="block">{option.label}</span>
                  {option.description && <span className="block text-[11.5px] text-muted-foreground">{option.description}</span>}
                </span>
                <span
                  aria-hidden="true"
                  className={`grid size-4 shrink-0 place-items-center border ${question.multi_select ? "rounded" : "rounded-full"} ${
                    checked ? "border-state-needs bg-state-needs text-background" : "border-muted-foreground/50"
                  }`}
                >
                  {checked && <Check className="size-3" strokeWidth={3} />}
                </span>
              </button>
            );
          })}
        </div>
      )}
      {acceptsText(question) && (
        <label className="grid gap-1">
          <span className="sr-only">{options.length ? "Your own answer" : "Your answer"}</span>
          <input
            ref={textRef}
            type={question.secret ? "password" : "text"}
            autoComplete="off"
            spellCheck={!question.secret}
            value={draft.text}
            disabled={sending}
            onChange={(event) => update({ ...draft, text: event.target.value })}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                next();
              }
            }}
            placeholder={question.secret ? "Hidden answer" : options.length ? "Or type your own answer" : "Type your answer"}
            className="h-10 w-full rounded-lg border bg-background px-2.5 text-[13px] outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/20"
          />
        </label>
      )}
      <div className="flex items-center gap-2 pt-0.5">
        <span className="min-w-0 flex-1 text-[11px] text-destructive" role="alert">{stepError}</span>
        {step > 0 && (
          <button
            type="button"
            onClick={() => goTo(step - 1)}
            disabled={sending}
            className="h-10 rounded-lg border px-3 text-[13px] outline-none transition hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
          >
            Back
          </button>
        )}
        <button
          type="button"
          onClick={next}
          disabled={sending}
          className="h-10 rounded-lg bg-state-needs px-4 text-[13px] font-semibold text-background outline-none transition hover:opacity-90 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60"
        >
          {last ? (questions.length > 1 ? "Submit answers" : "Submit answer") : "Next"}
        </button>
      </div>
    </div>
  );
}
