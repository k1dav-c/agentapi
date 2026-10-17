"use client";

import { useEffect, useId, useRef, useState } from "react";
import { Check } from "lucide-react";
import { ChoiceMark, PrimaryButton, TONE_CLASS, optionRowClass } from "./decision-card";
import { Kbd } from "./ui/kbd";
import { isImeComposing } from "@/lib/keys";
import { useNumberKeys } from "@/lib/use-number-keys";
import {
  acceptsText,
  buildQuestionResponse,
  emptyDrafts,
  questionError,
  toggleOption,
  type NativeRequest,
  type QuestionDraft,
  type RequestResponse,
} from "@/lib/native-requests";

// A question request: one step per question, then all answers at once.
export function QuestionSteps({
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
  const errorId = useId();
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
                <ChoiceMark checked={checked} round={!question.multi_select} />
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
              if (event.key === "Enter" && !isImeComposing(event)) {
                event.preventDefault();
                next();
              }
            }}
            aria-invalid={stepError ? true : undefined}
            aria-describedby={stepError ? errorId : undefined}
            placeholder={question.secret ? "Hidden answer" : options.length ? "Or type your own answer" : "Type your answer"}
            className="h-10 w-full rounded-lg border bg-background px-2.5 text-[13px] outline-none transition focus:border-ring focus:ring-2 focus:ring-ring/20"
          />
        </label>
      )}
      <div className="flex items-center gap-2 pt-0.5">
        <span id={errorId} className="min-w-0 flex-1 text-[11px] text-destructive" role="alert">{stepError}</span>
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
        <PrimaryButton onClick={next} disabled={sending} className="h-10 px-4">
          {last ? (questions.length > 1 ? "Submit answers" : "Submit answer") : "Next"}
        </PrimaryButton>
      </div>
    </div>
  );
}
