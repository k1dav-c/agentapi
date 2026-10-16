// Requests an agent on the native transport waits on (tool approvals,
// questions, plans), as served by GET /requests and the request_update
// event, and the logic to answer them.

export type RequestKind = "approval" | "question" | "plan";
export type OptionKind = "allow" | "allow_always" | "deny" | "cancel" | "other";
export type ResponseCapability = "live" | "message" | "not_resumable";
export type RequestStatus = "pending" | "resolved" | "withdrawn" | "expired";

export interface RequestOption {
  id: string;
  label: string;
  description?: string;
  kind: OptionKind;
}

export interface RequestQuestion {
  id: string;
  header?: string;
  question: string;
  options?: {label: string; description?: string}[];
  multi_select?: boolean;
  free_text?: boolean;
  secret?: boolean;
}

export interface RequestResponse {
  option_id?: string;
  answers?: Record<string, string[]>;
  text?: string;
}

export interface NativeRequest {
  id: string;
  kind: RequestKind;
  title: string;
  detail?: string;
  tool_name?: string;
  tool_use_id?: string;
  input?: unknown;
  options?: RequestOption[];
  questions?: RequestQuestion[];
  feedback?: boolean;
  response_capability: ResponseCapability;
  status: RequestStatus;
  response?: RequestResponse;
  created_at: string;
  closed_at?: string;
  // Change sequence of the request_update this state came from; absent for
  // states read from the REST API.
  seq?: number;
}

// How many closed requests the client keeps; the server keeps 50.
const MAX_CLOSED = 100;

// Applies one update to the stored state of a request. Events carry a seq
// and arrive in order, so a later seq always wins (that includes a request
// the agent reopens with the same id). A state read from the REST API (an
// answer's response, a refresh after a conflict) has no seq: it may only
// close a request the client still holds as pending, never reopen one.
function applyUpdate(existing: NativeRequest | undefined, update: NativeRequest): NativeRequest {
  if (!existing) return update;
  if (update.seq !== undefined) {
    return existing.seq !== undefined && update.seq < existing.seq ? existing : update;
  }
  if (existing.status === "pending" && update.status !== "pending") return {...update, seq: existing.seq};
  return existing;
}

// Drops the answers to secret questions, so they are neither kept in memory
// nor written to the session cache.
function redactSecrets(request: NativeRequest): NativeRequest {
  const secret = request.questions?.filter((question) => question.secret).map((question) => question.id) ?? [];
  const answers = request.response?.answers;
  if (!answers || !secret.some((id) => id in answers)) return request;
  const redacted = {...answers};
  for (const id of secret) if (id in redacted) redacted[id] = [];
  return {...request, response: {...request.response, answers: redacted}};
}

// Merges request updates into the list, keeping first-seen order and
// dropping the oldest closed requests beyond MAX_CLOSED.
export function mergeRequests(previous: NativeRequest[], updates: Iterable<NativeRequest>): NativeRequest[] {
  const next = previous.slice();
  const index = new Map(next.map((request, i) => [request.id, i]));
  for (const raw of updates) {
    const update = redactSecrets(raw);
    const at = index.get(update.id);
    if (at === undefined) {
      index.set(update.id, next.length);
      next.push(update);
    } else {
      next[at] = applyUpdate(next[at], update);
    }
  }
  let closed = next.filter((request) => request.status !== "pending").length;
  if (closed <= MAX_CLOSED) return next;
  return next.filter((request) => request.status === "pending" || closed-- <= MAX_CLOSED);
}

// The requests the agent is waiting on, oldest first.
export function pendingRequests(requests: NativeRequest[]): NativeRequest[] {
  return requests.filter((request) => request.status === "pending");
}

export type Tone = "needs" | "ok" | "fault" | "muted";

// What a tool call's row says about the request attached to it.
export function toolRequestBadge(request: NativeRequest): {label: string; tone: Tone} {
  switch (request.status) {
    case "pending":
      return {
        label: request.kind === "question" ? "Waiting for your answer" : request.kind === "plan" ? "Waiting for plan approval" : "Waiting for approval",
        tone: "needs",
      };
    case "resolved": {
      if (request.kind === "question" && !request.response?.option_id) return {label: "Answered", tone: "ok"};
      const option = request.options?.find((candidate) => candidate.id === request.response?.option_id);
      switch (option?.kind) {
        case "allow":
          return {label: request.kind === "plan" ? "Plan approved" : "Approved", tone: "ok"};
        case "allow_always":
          return {label: "Always allowed", tone: "ok"};
        case "deny":
          return {label: request.kind === "plan" ? "Plan rejected" : "Denied", tone: "fault"};
        case "cancel":
          return {label: "Cancelled", tone: "fault"};
        default:
          return {label: option?.label ?? "Answered", tone: "muted"};
      }
    }
    case "withdrawn":
      return {label: "No longer asked", tone: "muted"};
    case "expired":
      return {label: "Not answered", tone: "muted"};
  }
}

// How an option button is styled: allowing is the primary action, denying
// the destructive one.
export function optionTone(kind: OptionKind): "primary" | "secondary" | "destructive" | "neutral" {
  switch (kind) {
    case "allow":
      return "primary";
    case "allow_always":
      return "secondary";
    case "deny":
      return "destructive";
    case "cancel":
    case "other":
      return "neutral";
  }
}

// Whether feedback typed on the card goes with this option. Feedback says
// what to do instead, so it goes with a refusal.
export function optionTakesFeedback(request: NativeRequest, option: RequestOption): boolean {
  return Boolean(request.feedback) && (option.kind === "deny" || option.kind === "cancel");
}

// The response for picking an option of an approval or plan request.
export function optionResponse(request: NativeRequest, option: RequestOption, feedback: string): RequestResponse {
  const text = feedback.trim();
  return optionTakesFeedback(request, option) && text ? {option_id: option.id, text} : {option_id: option.id};
}

// What the user has entered for one question.
export interface QuestionDraft {
  selected: string[];
  text: string;
}

export function emptyDrafts(request: NativeRequest): Record<string, QuestionDraft> {
  return Object.fromEntries((request.questions ?? []).map((question) => [question.id, {selected: [], text: ""}]));
}

// Picks an option: a single-select question takes this one, a multi-select
// question toggles it.
export function toggleOption(question: RequestQuestion, draft: QuestionDraft, label: string): QuestionDraft {
  if (!question.multi_select) return {selected: [label], text: ""};
  const selected = draft.selected.includes(label)
    ? draft.selected.filter((item) => item !== label)
    : [...draft.selected, label];
  return {...draft, selected};
}

// Whether the question takes typed text: free text is allowed, or there is
// nothing to choose from.
export function acceptsText(question: RequestQuestion): boolean {
  return Boolean(question.free_text) || !question.options?.length;
}

// The answer to a question: typed text replaces the choice of a
// single-select question and adds to the choices of a multi-select one.
export function questionAnswer(question: RequestQuestion, draft: QuestionDraft | undefined): string[] {
  const text = acceptsText(question) ? draft?.text.trim() ?? "" : "";
  const selected = (draft?.selected ?? []).filter((label) => question.options?.some((option) => option.label === label));
  if (!question.multi_select) return text ? [text] : selected.slice(0, 1);
  return text ? [...selected, text] : selected;
}

// Why the question can't be submitted yet, or null.
export function questionError(question: RequestQuestion, draft: QuestionDraft | undefined): string | null {
  if (questionAnswer(question, draft).length > 0) return null;
  if (!question.options?.length) return "Type an answer";
  return question.multi_select ? "Choose at least one answer" : "Choose an answer";
}

// The response to a question request, or the first question that still
// needs an answer.
export function buildQuestionResponse(
  request: NativeRequest,
  drafts: Record<string, QuestionDraft>,
): {response: RequestResponse} | {error: string; question: number} {
  const answers: Record<string, string[]> = {};
  const questions = request.questions ?? [];
  for (let i = 0; i < questions.length; i++) {
    const question = questions[i];
    const error = questionError(question, drafts[question.id]);
    if (error) return {error, question: i};
    answers[question.id] = questionAnswer(question, drafts[question.id]);
  }
  return {response: {answers}};
}

// A question the agent stopped waiting for that can still be answered with
// a normal message: the latest request, closed after the last message sent.
export function answerByMessageHint(requests: NativeRequest[], lastUserMessageTime?: string): NativeRequest | null {
  const latest = requests.at(-1);
  if (!latest || latest.status !== "expired" || latest.response_capability !== "message") return null;
  if (lastUserMessageTime && latest.closed_at && Date.parse(lastUserMessageTime) > Date.parse(latest.closed_at)) return null;
  return latest;
}

// Error from POST /requests/{id}/respond, with the HTTP status.
export class RespondError extends Error {
  constructor(message: string, readonly status: number) {
    super(message);
    this.name = "RespondError";
  }
}

// What to tell the user about a failed answer.
export function respondErrorMessage(error: unknown): string {
  if (error instanceof RespondError) {
    if (error.status === 409) return "This was already answered or is no longer pending.";
    if (error.status === 404) return "The agent no longer has this request.";
    return error.message;
  }
  return error instanceof Error ? error.message : String(error);
}
