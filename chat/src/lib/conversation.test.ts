import {describe, expect, test} from "bun:test";
import {commonPrefix, isDraftMessage, lastUserMessage, normalizeRichMessage, richMessageKey, richMessagesSince, withoutSeq, type DraftMessage, type Message, type RichMessage, type RichMessagePayload} from "./conversation";

describe("conversation", () => {
  test("a message without a server ID is a draft", () => {
    const sent: Message = {id: 3, role: "user", content: "hi"};
    const draft: DraftMessage = {role: "user", content: "hi", clientId: "c1", deliveryStatus: "sending"};
    expect(isDraftMessage(sent)).toBe(false);
    expect(isDraftMessage(draft)).toBe(true);
    expect([sent, draft].filter(isDraftMessage).map((message) => message.clientId)).toEqual(["c1"]);
  });

  test("a rich message is keyed by its ID and role", () => {
    const message = {message_id: "m1", role: "assistant", content: [], timestamp: ""};
    expect(richMessageKey(message)).not.toBe(richMessageKey({...message, role: "user"}));
    expect(richMessageKey(message)).toBe(richMessageKey({...message, content: [{type: "text", text: "x"}]}));
  });

  test("withoutSeq drops the sequence and leaves the item alone", () => {
    const update = {id: 1, role: "agent", content: "a", seq: 9};
    expect(withoutSeq(update)).toEqual({id: 1, role: "agent", content: "a"});
    expect(update.seq).toBe(9);
  });
});

describe("the current task", () => {
  test("is the user's last message", () => {
    const messages: (Message | DraftMessage)[] = [
      {id: 0, role: "user", content: "first"},
      {id: 1, role: "agent", content: "done"},
      {role: "user", content: "second", clientId: "c1", deliveryStatus: "sending"},
      {id: 2, role: "agent", content: "working"},
    ];
    expect(lastUserMessage(messages)?.content).toBe("second");
    expect(lastUserMessage([{id: 0, role: "agent", content: "hello"}])).toBeUndefined();
  });
});

describe("normalizeRichMessage", () => {
  test("turns null content into no blocks", () => {
    // What Go sends for a message without content blocks, such as a Pi
    // tool result the adapter couldn't parse.
    const payload = JSON.parse('{"message_id":"m1","role":"user","content":null,"timestamp":"2026-10-22T10:00:00Z","seq":7}');
    const message = normalizeRichMessage<RichMessagePayload & {seq: number}>(payload);
    expect(message.content).toEqual([]);
    expect(message.seq).toBe(7);
    expect(normalizeRichMessage({message_id: "m2", role: "assistant", timestamp: ""} as unknown as RichMessagePayload).content).toEqual([]);
  });

  test("turns a tool result's null images into none", () => {
    const message = normalizeRichMessage({
      message_id: "m1",
      role: "user",
      timestamp: "",
      content: [{type: "tool_result", tool_use_id: "t1", text: "ok", images: null}],
    });
    expect(message.content[0].images).toEqual([]);
  });

  test("a message with nothing null keeps its object", () => {
    const message: RichMessagePayload = {message_id: "m1", role: "assistant", timestamp: "", content: [{type: "text", text: "hi"}]};
    expect(Object.is(normalizeRichMessage(message), message)).toBe(true);
  });
});

describe("finding what changed", () => {
  test("the common start of two lists is the items that are the same objects", () => {
    const a = {id: 1};
    const b = {id: 2};
    expect(commonPrefix([a, b], [a, b, {id: 3}])).toBe(2);
    expect(commonPrefix([a, b], [a, {id: 2}])).toBe(1);
    expect(commonPrefix([], [a])).toBe(0);
  });

  test("the current task's rich messages are those from its prompt's time", () => {
    const at = (id: string, timestamp: string): RichMessage => ({message_id: id, role: "assistant", timestamp, content: []});
    const messages = [at("old", "2026-10-22T09:59:00Z"), at("a", "2026-10-22T10:00:01Z"), at("bad", "not a time"), at("b", "2026-10-22T10:00:02Z")];
    expect(richMessagesSince(messages, "2026-10-22T10:00:00Z").map((m) => m.message_id)).toEqual(["a", "b"]);
    expect(richMessagesSince(messages, "2026-10-22T11:00:00Z")).toEqual([]);
    // A task not sent yet has none.
    expect(richMessagesSince(messages, undefined)).toEqual([]);
  });
});
