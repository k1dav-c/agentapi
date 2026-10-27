// Requests an agent on the native transport waits on (tool approvals,
// questions, plans), as served by GET /requests and the request_update
// event, and the logic to answer them.

import {ApiError} from "./error-utils";
import {getToolCommand} from "./tool-format";
import type {components} from "./api-schema";

// The API's types, generated from openapi.json (lib/api-schema.ts).
type Schemas = components["schemas"];

export type RequestKind = Schemas["RequestKind"];
export type OptionKind = Schemas["OptionKind"];
export type ResponseCapability = Schemas["ResponseCapability"];
export type RequestStatus = Schemas["RequestStatus"];
export type RequestOption = Schemas["RequestOption"];
export type RequestQuestion = Schemas["Question"];
// An answer to a request (POST /requests/{id}/respond).
export interface RequestResponse {
  option_id?: string;
  answers?: Record<string, string[]>;
  text?: string;
}

// A request and what became of it, as the server keeps it.
export type NativeRequest = Omit<Schemas["RequestState"], "$schema" | "response"> & {
  response?: RequestResponse;
  // Change sequence of the request_update this state came from; absent for
  // states read from the REST API.
  seq?: number;
};

// A request as the server sends it (GET /requests, the request_update
// event): Go encodes an answer without values as null.
export type RequestPayload = Omit<Schemas["RequestState"], "$schema"> & {seq?: number};

// normalizeRequest returns request with empty lists for null answers; a
// request that has none keeps its object.
export function normalizeRequest<T extends RequestPayload>(request: T): Omit<T, "response"> & {response?: RequestResponse} {
  const answers = request.response?.answers;
  if (!answers || Object.values(answers).every((values) => values !== null)) {
    return request as Omit<T, "response"> & {response?: RequestResponse};
  }
  const fixed = Object.fromEntries(Object.entries(answers).map(([id, values]) => [id, values ?? []]));
  return {...request, response: {...request.response, answers: fixed}};
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
// dropping the closed requests beyond MAX_CLOSED that closed first.
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
  const closed = next.filter((request) => request.status !== "pending");
  if (closed.length <= MAX_CLOSED) return next;
  const closedAt = (request: NativeRequest) => Date.parse(request.closed_at ?? "") || 0;
  const dropped = new Set(closed.sort((a, b) => closedAt(a) - closedAt(b)).slice(0, closed.length - MAX_CLOSED));
  return next.filter((request) => !dropped.has(request));
}

// Merges the list GET /requests returned. A request the client held as
// pending when it asked (heldWhenAsked) that the list leaves out is gone
// from the server: its close happened while the client was away and was
// trimmed since, or the server restarted without --state-db. No answer can
// reach it any more, so it is closed as withdrawn. A request that opened or
// changed after the client asked is left alone.
export function mergeListedRequests(
  previous: NativeRequest[],
  listed: NativeRequest[],
  heldWhenAsked: NativeRequest[],
): NativeRequest[] {
  const listedIds = new Set(listed.map((request) => request.id));
  const current = new Map(previous.map((request) => [request.id, request]));
  const gone = heldWhenAsked.filter((asked) => {
    const now = current.get(asked.id);
    return asked.status === "pending" && !listedIds.has(asked.id) && now?.status === "pending" && now.seq === asked.seq;
  });
  return mergeRequests(previous, [
    ...listed,
    // Without a seq, like any state read from the REST API: it may close the
    // request but never reopen one.
    ...gone.map((request): NativeRequest => ({...request, status: "withdrawn", seq: undefined})),
  ]);
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
    case "expired":
      return {label: "Not answered", tone: "muted"};
    // A status a newer server added isn't pending: the request is closed.
    case "withdrawn":
    default:
      return {label: "No longer asked", tone: "muted"};
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
    // And a kind a newer server added.
    case "cancel":
    case "other":
    default:
      return "neutral";
  }
}

// Whether feedback typed on the card goes with this option. Feedback says
// what to do instead, so it goes with a refusal.
export function optionTakesFeedback(request: NativeRequest, option: RequestOption): boolean {
  return Boolean(request.feedback) && (option.kind === "deny" || option.kind === "cancel");
}

// Commands this long (or on several lines) are shown in full under an
// approval's title even when the title has them: it shows two lines at most.
const LONG_COMMAND = 120;

// The command an approval runs, to show under its title: unless the title
// shows all of it (it has the command, which is short, and isn't cut off at
// two lines). A Codex approval's title may only summarize a long command,
// which is in the input.
export function approvalCommand(
  request: Pick<NativeRequest, "kind" | "title" | "tool_name" | "input">,
  titleClamped: boolean,
): string | undefined {
  if (request.kind !== "approval") return undefined;
  const command = getToolCommand(request.tool_name ?? "", request.input);
  if (!command) return undefined;
  const long = command.length > LONG_COMMAND || command.includes("\n");
  return request.title.includes(command) && !long && !titleClamped ? undefined : command;
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

// A question the agent stopped waiting for (when its turn ended, or it
// withdrew the question) that a normal message can still answer: the latest
// request, closed after the last message sent.
export function answerByMessageHint(requests: NativeRequest[], lastUserMessageTime?: string): NativeRequest | null {
  const latest = requests.at(-1);
  if (!latest || latest.response_capability !== "message") return null;
  if (lastUserMessageTime && latest.closed_at && Date.parse(lastUserMessageTime) > Date.parse(latest.closed_at)) return null;
  return latest;
}

// What to tell the user about a failed answer (POST /requests/{id}/respond).
export function respondErrorMessage(error: unknown): string {
  if (error instanceof ApiError) {
    if (error.status === 409) return "This was already answered or is no longer pending.";
    if (error.status === 404) return "The agent no longer has this request.";
  }
  return error instanceof Error ? error.message : String(error);
}
