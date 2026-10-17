import {describe, expect, test} from "bun:test";
import {ApiError} from "./error-utils";
import {
  answerByMessageHint,
  buildQuestionResponse,
  emptyDrafts,
  mergeListedRequests,
  mergeRequests,
  optionResponse,
  optionTone,
  pendingRequests,
  questionAnswer,
  questionError,
  respondErrorMessage,
  toggleOption,
  toolRequestBadge,
  type NativeRequest,
  type RequestQuestion,
} from "./native-requests";

const approval = (overrides: Partial<NativeRequest> = {}): NativeRequest => ({
  id: "req-1",
  kind: "approval",
  title: "Run make release",
  tool_name: "Bash",
  tool_use_id: "toolu_1",
  options: [
    {id: "allow", label: "Allow", kind: "allow"},
    {id: "always", label: "Always allow", kind: "allow_always"},
    {id: "deny", label: "Deny", kind: "deny"},
  ],
  feedback: true,
  response_capability: "live",
  status: "pending",
  created_at: "2026-10-16T10:00:00Z",
  ...overrides,
});

const questions: RequestQuestion[] = [
  {id: "pm", question: "Package manager?", options: [{label: "bun"}, {label: "npm"}]},
  {id: "checks", question: "Checks?", multi_select: true, options: [{label: "Lint"}, {label: "Tests"}]},
  {id: "notes", question: "Notes?", free_text: true},
];

const question = (overrides: Partial<NativeRequest> = {}): NativeRequest => ({
  id: "req-2",
  kind: "question",
  title: "Questions",
  questions,
  response_capability: "live",
  status: "pending",
  created_at: "2026-10-16T10:01:00Z",
  ...overrides,
});

describe("mergeRequests", () => {
  test("adds new requests in order and replaces them by id", () => {
    const merged = mergeRequests([], [approval({seq: 3}), question({seq: 4}), approval({seq: 5, status: "resolved"})]);
    expect(merged.map((request) => [request.id, request.status])).toEqual([["req-1", "resolved"], ["req-2", "pending"]]);
    expect(pendingRequests(merged).map((request) => request.id)).toEqual(["req-2"]);
  });

  test("ignores an event older than the state it holds", () => {
    const merged = mergeRequests([approval({seq: 7, status: "resolved"})], [approval({seq: 5})]);
    expect(merged[0].status).toBe("resolved");
  });

  test("a later event reopens a request with the same id", () => {
    const merged = mergeRequests([approval({seq: 5, status: "expired"})], [approval({seq: 9})]);
    expect(merged[0].status).toBe("pending");
  });

  test("a state without seq (REST) closes a pending request but never reopens one", () => {
    const answered = mergeRequests([approval({seq: 5})], [approval({status: "resolved", response: {option_id: "allow"}})]);
    expect(answered[0]).toMatchObject({status: "resolved", seq: 5});
    // The event for the same change arrives afterwards and still applies.
    expect(mergeRequests(answered, [approval({seq: 6, status: "resolved"})])[0].seq).toBe(6);
    // A stale REST read can't undo the answer.
    expect(mergeRequests(answered, [approval()])[0].status).toBe("resolved");
  });

  test("replaying a cached state with the changes since its seq gives the server's state", () => {
    // The cache was saved with req-1 pending at seq 5; while the tab was
    // closed req-1 was answered and req-2 opened. ?since=5 replays both.
    const cached = [approval({seq: 5})];
    const replay = [approval({seq: 8, status: "resolved"}), question({seq: 9})];
    const merged = mergeRequests(cached, replay);
    expect(merged.map((request) => `${request.id}:${request.status}:${request.seq}`)).toEqual([
      "req-1:resolved:8",
      "req-2:pending:9",
    ]);
  });

  test("drops the oldest closed requests beyond the cap", () => {
    const closed = Array.from({length: 105}, (_, i) => approval({id: `c${i}`, status: "resolved", seq: i + 1}));
    const merged = mergeRequests([], [...closed, question({seq: 200})]);
    expect(merged).toHaveLength(101);
    expect(merged[0].id).toBe("c5");
    expect(merged.at(-1)?.id).toBe("req-2");
  });

  test("does not keep the answers to secret questions", () => {
    const secret = question({
      questions: [{id: "token", question: "Token?", free_text: true, secret: true}, questions[0]],
      status: "resolved",
      response: {answers: {token: ["hunter2"], pm: ["bun"]}},
      seq: 3,
    });
    const [stored] = mergeRequests([], [secret]);
    expect(stored.response?.answers).toEqual({token: [], pm: ["bun"]});
    expect(JSON.stringify(stored)).not.toContain("hunter2");
  });
});

describe("mergeListedRequests", () => {
  test("applies the listed states like any REST state", () => {
    const merged = mergeListedRequests([approval({seq: 5})], [approval({status: "resolved"}), question()], [approval({seq: 5})]);
    expect(merged.map((request) => `${request.id}:${request.status}:${request.seq}`)).toEqual(["req-1:resolved:5", "req-2:pending:undefined"]);
  });

  test("withdraws a pending request the server no longer lists", () => {
    const held = [approval({seq: 5}), question({seq: 6, status: "resolved"})];
    const merged = mergeListedRequests(held, [], held);
    expect(merged.map((request) => `${request.id}:${request.status}:${request.seq}`)).toEqual(["req-1:withdrawn:5", "req-2:resolved:6"]);
    expect(pendingRequests(merged)).toEqual([]);
  });

  test("leaves a request that opened or changed after the client asked", () => {
    const asked = [approval({seq: 5})];
    // req-2 opened and req-1 was reopened (a later seq) while GET /requests
    // was on its way; the list was read before either.
    const now = [approval({seq: 9}), question({seq: 8})];
    expect(pendingRequests(mergeListedRequests(now, [], asked)).map((request) => request.id)).toEqual(["req-1", "req-2"]);
  });
});

describe("toolRequestBadge", () => {
  test("describes each state of the request linked to a tool call", () => {
    expect(toolRequestBadge(approval())).toEqual({label: "Waiting for approval", tone: "needs"});
    expect(toolRequestBadge(approval({status: "resolved", response: {option_id: "allow"}})).label).toBe("Approved");
    expect(toolRequestBadge(approval({status: "resolved", response: {option_id: "always"}})).label).toBe("Always allowed");
    expect(toolRequestBadge(approval({status: "resolved", response: {option_id: "deny", text: "no"}}))).toEqual({label: "Denied", tone: "fault"});
    expect(toolRequestBadge(approval({kind: "plan", status: "resolved", response: {option_id: "deny"}})).label).toBe("Plan rejected");
    expect(toolRequestBadge(question({status: "resolved", response: {answers: {}}})).label).toBe("Answered");
    expect(toolRequestBadge(question()).label).toBe("Waiting for your answer");
    expect(toolRequestBadge(approval({status: "expired"})).tone).toBe("muted");
  });
});

describe("option responses", () => {
  test("styles options by kind", () => {
    expect(optionTone("allow")).toBe("primary");
    expect(optionTone("allow_always")).toBe("secondary");
    expect(optionTone("deny")).toBe("destructive");
    expect(optionTone("other")).toBe("neutral");
  });

  test("sends feedback only with a refusal, and only when the request takes it", () => {
    const request = approval();
    const [allow, , deny] = request.options!;
    expect(optionResponse(request, deny, "  use make build ")).toEqual({option_id: "deny", text: "use make build"});
    expect(optionResponse(request, deny, "  ")).toEqual({option_id: "deny"});
    expect(optionResponse(request, allow, "ignored")).toEqual({option_id: "allow"});
    expect(optionResponse(approval({feedback: false}), deny, "ignored")).toEqual({option_id: "deny"});
  });
});

describe("question answers", () => {
  test("single select takes one option; typed text replaces it", () => {
    const [single] = questions;
    let draft = toggleOption(single, {selected: [], text: ""}, "bun");
    draft = toggleOption(single, draft, "npm");
    expect(questionAnswer(single, draft)).toEqual(["npm"]);
    // Text is only an answer when the question allows it.
    expect(questionAnswer(single, {selected: ["npm"], text: "yarn"})).toEqual(["npm"]);
    expect(questionAnswer({...single, free_text: true}, {selected: ["npm"], text: "yarn"})).toEqual(["yarn"]);
  });

  test("multi select toggles options and adds typed text", () => {
    const multi = {...questions[1], free_text: true};
    let draft = toggleOption(multi, {selected: [], text: ""}, "Lint");
    draft = toggleOption(multi, draft, "Tests");
    draft = toggleOption(multi, draft, "Lint");
    expect(questionAnswer(multi, {...draft, text: "e2e"})).toEqual(["Tests", "e2e"]);
  });

  test("explains what an unanswered question needs", () => {
    expect(questionError(questions[0], {selected: [], text: ""})).toBe("Choose an answer");
    expect(questionError(questions[1], {selected: [], text: ""})).toBe("Choose at least one answer");
    expect(questionError(questions[2], {selected: [], text: "  "})).toBe("Type an answer");
    expect(questionError(questions[2], {selected: [], text: "ok"})).toBeNull();
  });

  test("builds all answers, or points at the first unanswered question", () => {
    const request = question();
    const drafts = emptyDrafts(request);
    drafts.pm = {selected: ["bun"], text: ""};
    expect(buildQuestionResponse(request, drafts)).toEqual({error: "Choose at least one answer", question: 1});
    drafts.checks = {selected: ["Lint", "Tests"], text: ""};
    drafts.notes = {selected: [], text: " none "};
    expect(buildQuestionResponse(request, drafts)).toEqual({
      response: {answers: {pm: ["bun"], checks: ["Lint", "Tests"], notes: ["none"]}},
    });
  });
});

describe("answerByMessageHint", () => {
  test("offers a normal message for the latest question the agent stopped waiting for", () => {
    const expired = question({status: "expired", response_capability: "message", closed_at: "2026-10-16T10:05:00Z"});
    expect(answerByMessageHint([approval({status: "resolved"}), expired])).toBe(expired);
    expect(answerByMessageHint([expired], "2026-10-16T10:06:00Z")).toBeNull();
    expect(answerByMessageHint([{...expired, response_capability: "not_resumable"}])).toBeNull();
    expect(answerByMessageHint([question()])).toBeNull();
  });

  test("a question the agent withdrew can be answered by message too", () => {
    const withdrawn = question({status: "withdrawn", response_capability: "message", closed_at: "2026-10-16T10:05:00Z"});
    expect(answerByMessageHint([withdrawn])).toBe(withdrawn);
    expect(answerByMessageHint([{...withdrawn, response_capability: "live"}])).toBeNull();
  });
});

describe("respondErrorMessage", () => {
  test("explains conflicts", () => {
    expect(respondErrorMessage(new ApiError("request is resolved, not pending", 409))).toBe(
      "This was already answered or is no longer pending.",
    );
    expect(respondErrorMessage(new ApiError("request not found", 404))).toBe("The agent no longer has this request.");
    expect(respondErrorMessage(new ApiError("bad answer", 400))).toBe("bad answer");
    expect(respondErrorMessage(new Error("offline"))).toBe("offline");
  });
});
