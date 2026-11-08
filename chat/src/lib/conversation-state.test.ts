import {describe, expect, test} from "bun:test";
import {conversationReducer, initialConversation, mergeByKey, messagesAfter, richMessagesAfter, type ConversationAction, type ConversationState} from "./conversation-state";
import {withoutSeq} from "./conversation";
import type {CachedSession} from "./session-cache";
import {batch, draft, message, prompt, request, rich, task} from "@/test/conversation";

const apply = (state: ConversationState, ...actions: ConversationAction[]) => actions.reduce(conversationReducer, state);
const at = (epoch: string, seq: number) => ({epoch, seq});

describe("conversationReducer", () => {
  test("a batch applies the transcript and its point; the requests come apart", () => {
    const update = batch({messages: [prompt(0, 1), message(1, 2)], richMessages: [rich("r1", 3)], tasks: [task(0, 4)], requests: [request("q1", "pending", 5)], point: at("e1", 5)});
    const state = apply(initialConversation, {type: "batch", batch: update});
    expect(state.messages).toEqual([withoutSeq(prompt(0, 1)), withoutSeq(message(1, 2))]);
    expect(state.richMessages).toEqual([withoutSeq(rich("r1", 3))]);
    expect(state.tasks.map((item) => item.id)).toEqual([0]);
    expect(state.synced).toEqual(at("e1", 5));
    expect(state.requests).toEqual([]);
    const both = apply(state, {type: "batchRequests", batch: update});
    expect(both.requests.map((item) => item.id)).toEqual(["q1"]);
    expect(both.messages).toBe(state.messages);
  });

  test("the two parts of a batch give the same state in either order", () => {
    // The requests apply at once, the transcript in a transition that a
    // render may apply after them.
    const update = batch({messages: [prompt(0, 1)], requests: [request("q1", "pending", 2)], point: at("e1", 2)});
    const transcript: ConversationAction = {type: "batch", batch: update};
    const requests: ConversationAction = {type: "batchRequests", batch: update};
    expect(apply(initialConversation, requests, transcript)).toEqual(apply(initialConversation, transcript, requests));
  });

  test("a batch that changes nothing keeps the state, and an unchanged list its object", () => {
    const state = apply(initialConversation, {type: "batch", batch: batch({messages: [prompt(0, 1)], richMessages: [rich("r1", 2)], point: at("e1", 2)})});
    expect(apply(state, {type: "batch", batch: batch({point: at("e1", 2)})})).toBe(state);
    expect(apply(state, {type: "batchRequests", batch: batch({point: at("e1", 2)})})).toBe(state);
    const later = apply(state, {type: "batch", batch: batch({richMessages: [rich("r2", 3)], point: at("e1", 3)})});
    expect(later.messages).toBe(state.messages);
    expect(later.richMessages).not.toBe(state.richMessages);
    expect(later.synced).toEqual(at("e1", 3));
  });

  test("batches apply in order: a later update of a message replaces it in place", () => {
    const first = batch({messages: [prompt(0, 1), message(1, 2)], point: at("e1", 2)});
    const second = batch({messages: [{...message(1, 3), content: "edited"}, message(2, 4)], point: at("e1", 4)});
    const state = apply(initialConversation, {type: "batch", batch: first}, {type: "batch", batch: second});
    expect(state.messages.map((item) => item.content)).toEqual(["prompt 0", "edited", "message 2"]);
    expect(state.synced).toEqual(at("e1", 4));
  });

  test("a full replay of another epoch replaces the transcript and the point; failed drafts stay", () => {
    const failed = draft("c1", "lost", "failed");
    const old = apply(
      initialConversation,
      {type: "batch", batch: batch({messages: [prompt(0, 1), message(1, 2)], richMessages: [rich("r1", 3)], point: at("e1", 3)})},
      {type: "draftAdded", draft: failed},
    );
    const state = apply(old, {type: "batch", batch: batch({replace: true, messages: [prompt(0, 1)], point: at("e2", 1)})});
    expect(state.messages).toEqual([failed, withoutSeq(prompt(0, 1))]);
    expect(state.richMessages).toEqual([]);
    expect(state.synced).toEqual(at("e2", 1));
  });

  test("a request event never goes back to an earlier seq; a state read over REST only closes", () => {
    const state = apply(initialConversation, {type: "batchRequests", batch: batch({requests: [request("q1", "pending", 5)]})});
    expect(apply(state, {type: "batchRequests", batch: batch({requests: [request("q1", "resolved", 4)]})}).requests[0].status).toBe("pending");
    const answered = apply(state, {type: "requestAnswered", request: request("q1", "resolved", undefined)});
    expect(answered.requests[0]).toMatchObject({status: "resolved", seq: 5});
    expect(apply(answered, {type: "requestAnswered", request: request("q1", "pending", undefined)}).requests[0].status).toBe("resolved");
  });

  test("a request listed as gone that was pending when asked is withdrawn", () => {
    const state = apply(initialConversation, {type: "batchRequests", batch: batch({requests: [request("q1", "pending", 5), request("q2", "pending", 6)]})});
    const listed = apply(state, {type: "requestsListed", listed: [request("q2", "pending", undefined)], heldWhenAsked: state.requests});
    expect(listed.requests.map((item) => [item.id, item.status])).toEqual([["q1", "withdrawn"], ["q2", "pending"]]);
  });

  test("restore shows the cache at its point and keeps the drafts", () => {
    const sending = draft("c1", "on its way");
    const cached: CachedSession = {
      epoch: "e1",
      seq: 9,
      messages: [withoutSeq(prompt(0, 1))],
      richMessages: [withoutSeq(rich("r1", 2))],
      requests: [request("q1", "resolved", 3)],
      tasks: [task(0, 4)],
    };
    const state = apply(initialConversation, {type: "draftAdded", draft: sending}, {type: "restore", cached});
    expect(state.messages).toEqual([withoutSeq(prompt(0, 1)), sending]);
    expect(state.richMessages).toBe(cached.richMessages);
    expect(state.requests).toBe(cached.requests);
    expect(state.tasks).toBe(cached.tasks);
    expect(state.synced).toEqual(at("e1", 9));
  });

  test("reset forgets the conversation and its point", () => {
    const state = apply(
      initialConversation,
      {type: "batch", batch: batch({messages: [prompt(0, 1)], tasks: [task(0, 2)], point: at("e1", 2)})},
      {type: "batchRequests", batch: batch({requests: [request("q1", "pending", 3)]})},
      {type: "draftAdded", draft: draft("c1", "hi")},
      {type: "reset"},
    );
    expect(state).toEqual(initialConversation);
    expect(apply(initialConversation, {type: "reset"}).synced).toBe(initialConversation.synced);
  });

  test("drafts: added, marked failed, dismissed, and restored once", () => {
    const sent = apply(initialConversation, {type: "draftAdded", draft: draft("c1", "hi")});
    const failed = apply(sent, {type: "draftFailed", clientId: "c1", error: "offline"});
    expect(failed.messages).toEqual([{...draft("c1", "hi"), deliveryStatus: "failed", error: "offline"}]);
    expect(apply(failed, {type: "draftRemoved", clientId: "c1"}).messages).toEqual([]);
    expect(apply(failed, {type: "draftRemoved", clientId: "other"})).toBe(failed);
    const restored = apply(initialConversation, {type: "draftsRestored", drafts: [draft("c1", "hi", "failed")]}, {type: "draftsRestored", drafts: [draft("c1", "hi", "failed")]});
    expect(restored.messages).toHaveLength(1);
  });
});

describe("richMessagesAfter", () => {
  test("an update replaces its own message, a full replay all of them", () => {
    const previous = [withoutSeq(rich("r1", 1)), withoutSeq(rich("r2", 2))];
    const updated = {...rich("r1", 3), timestamp: "2026-10-22T10:00:09Z"};
    expect(richMessagesAfter(previous, batch({richMessages: [updated]}))).toEqual([withoutSeq(updated), previous[1]]);
    expect(richMessagesAfter(previous, batch({replace: true, richMessages: [updated]}))).toEqual([withoutSeq(updated)]);
    expect(richMessagesAfter(previous, batch({}))).toBe(previous);
  });
});

describe("messagesAfter", () => {
  const sent = (id: number, content: string, seq: number) => ({id, role: "user" as const, content, time: "2026-10-22T10:00:00Z", seq});

  test("a draft still being sent stays through other updates, so a failed send can mark it", () => {
    // The agent streams its turn while a message waits for POST /message.
    const next = messagesAfter([prompt(0, 1), draft("c1", "next one")], batch({messages: [message(1, 2)]}));
    expect(next.map((item) => ("clientId" in item ? item.clientId : item.id))).toEqual([0, "c1", 1]);
  });

  test("the server's message with its content takes the draft's place", () => {
    const next = messagesAfter([prompt(0, 1), draft("c1", "next one ")], batch({messages: [sent(2, "next one", 3)]}));
    expect(next).toEqual([prompt(0, 1), {id: 2, role: "user", content: "next one", time: "2026-10-22T10:00:00Z"}]);
  });

  test("one message takes the place of one draft; an update of a message held already of none", () => {
    const drafts = [draft("c1", "yes"), draft("c2", "yes")];
    const next = messagesAfter([sent(4, "yes", 1), ...drafts], batch({messages: [sent(4, "yes", 5), sent(6, "yes", 6)]}));
    expect(next.filter((item) => "clientId" in item)).toEqual([drafts[1]]);
  });

  test("failed drafts stay, through a full replay too", () => {
    const failed = draft("c1", "lost", "failed");
    const sending = draft("c2", "on its way");
    const next = messagesAfter([prompt(0, 1), failed, sending], batch({replace: true, messages: [message(1, 2), sent(2, "lost", 3)]}));
    expect(next.slice(0, 2)).toEqual([failed, sending]);
    expect(next).toHaveLength(4);
  });
});

describe("mergeByKey", () => {
  const key = (item: {id?: number}) => (item.id === undefined ? undefined : String(item.id));

  test("replaces existing entries in place and appends new ones in order", () => {
    const previous = [{id: 1, v: "a"}, {id: 2, v: "b"}];
    const merged = mergeByKey(previous, [{id: 2, v: "B"}, {id: 3, v: "c"}, {id: 1, v: "A"}, {id: 3, v: "C"}], key);
    expect(merged).toEqual([{id: 1, v: "A"}, {id: 2, v: "B"}, {id: 3, v: "C"}]);
    expect(previous).toEqual([{id: 1, v: "a"}, {id: 2, v: "b"}]);
  });

  test("keeps keyless entries such as unsent drafts", () => {
    const previous = [{id: 1, v: "a"}, {v: "draft"}];
    expect(mergeByKey(previous, [{id: 2, v: "b"}], key)).toEqual([{id: 1, v: "a"}, {v: "draft"}, {id: 2, v: "b"}]);
  });

  test("handles a long replay in linear time", () => {
    const updates = Array.from({length: 20000}, (_, i) => ({id: i % 5000, v: String(i)}));
    const started = performance.now();
    const merged = mergeByKey([], updates, key);
    expect(merged).toHaveLength(5000);
    expect(merged[0]).toEqual({id: 0, v: "15000"});
    expect(performance.now() - started).toBeLessThan(200);
  });
});
