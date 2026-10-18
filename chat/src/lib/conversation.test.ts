import {describe, expect, test} from "bun:test";
import {isDraftMessage, lastUserMessage, richMessageKey, withoutSeq, type DraftMessage, type Message} from "./conversation";

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
