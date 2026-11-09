import {afterEach, describe, expect, spyOn, test} from "bun:test";
import {act, useReducer} from "react";
import type {ChatAPI, SendResult} from "@/lib/chat-api";
import {conversationReducer, initialConversation} from "@/lib/conversation-state";
import {withDOM} from "@/test/dom";
import {renderHook} from "@/test/render";
import {useDrafts} from "./use-drafts";

withDOM();

const url = "http://agent.test";
const storageKey = `agentapi.chat.failed-messages:${url}`;

// A ChatAPI whose POST /message answers with the next of answers (a
// promise to settle by hand, or an error), recording what was sent.
function fakeAPI() {
  const sent: string[] = [];
  const answers: {resolve: (result: SendResult) => void; reject: (error: Error) => void}[] = [];
  const api = {
    sendMessage: (content: string) => {
      sent.push(content);
      return new Promise<SendResult>((resolve, reject) => answers.push({resolve, reject}));
    },
  };
  return {api: api as unknown as ChatAPI, sent, answers};
}

// useDrafts on the conversation's reducer, as the chat runs it.
function useHarness({api, queueChanged}: {api: ChatAPI; queueChanged: () => Promise<void>}) {
  const [state, dispatch] = useReducer(conversationReducer, initialConversation);
  return {messages: state.messages, ...useDrafts({agentAPIUrl: url, api, messages: state.messages, dispatch, queueChanged})};
}

let unmount: (() => Promise<void>) | undefined;
afterEach(async () => {
  await unmount?.();
  unmount = undefined;
  localStorage.clear();
});

async function mountDrafts() {
  const fake = fakeAPI();
  let queueReads = 0;
  const hook = await renderHook(useHarness, {api: fake.api, queueChanged: async () => void queueReads++});
  unmount = hook.unmount;
  return {...fake, hook, queueReads: () => queueReads};
}

describe("useDrafts", () => {
  test("a message shows as sending until the server has it, and the queue is read again", async () => {
    const {hook, sent, answers, queueReads} = await mountDrafts();
    let result: Promise<SendResult> | undefined;
    await act(async () => void (result = hook.result.current.sendMessage("Fix the build")));
    expect(sent).toEqual(["Fix the build"]);
    expect(hook.result.current.loading).toBe(true);
    expect(hook.result.current.messages).toMatchObject([{role: "user", content: "Fix the build", deliveryStatus: "sending"}]);

    await act(async () => {
      answers[0].resolve({ok: true, queued: true});
      await result;
    });
    expect(await result).toEqual({ok: true, queued: true, steered: undefined});
    expect(queueReads()).toBe(1);
    expect(hook.result.current.messages).toEqual([]);
    expect(hook.result.current.loading).toBe(false);
  });

  test("nothing but spaces isn't sent", async () => {
    const {hook, sent} = await mountDrafts();
    let result: SendResult | undefined;
    await act(async () => void (result = await hook.result.current.sendMessage("  \n")));
    expect(result).toEqual({ok: false, queued: false});
    expect(sent).toEqual([]);
    expect(hook.result.current.messages).toEqual([]);
  });

  test("a failed send stays, kept across a reload, to send again or dismiss", async () => {
    const {hook, sent, answers} = await mountDrafts();
    // The failure is logged, besides its toast.
    const logged = spyOn(console, "error").mockImplementation(() => undefined);
    let result: Promise<SendResult> | undefined;
    await act(async () => void (result = hook.result.current.sendMessage("Deploy")));
    await act(async () => {
      answers[0].reject(new Error("The server is down"));
      await result;
    });
    expect(await result).toEqual({ok: false, queued: false});
    expect(logged).toHaveBeenCalledTimes(1);
    logged.mockRestore();
    const [failed] = hook.result.current.messages;
    expect(failed).toMatchObject({content: "Deploy", deliveryStatus: "failed", error: "The server is down"});
    expect(JSON.parse(localStorage.getItem(storageKey)!)).toMatchObject([{content: "Deploy", deliveryStatus: "failed"}]);

    // A reload shows it again.
    await unmount!();
    const reloaded = await mountDrafts();
    expect(reloaded.hook.result.current.messages).toMatchObject([{content: "Deploy", deliveryStatus: "failed"}]);
    const clientId = (reloaded.hook.result.current.messages[0] as {clientId: string}).clientId;

    // Sent again: it goes out as a new message, in place of the failed one.
    let retried: Promise<boolean> | undefined;
    await act(async () => void (retried = reloaded.hook.result.current.retryFailedMessage(clientId)));
    expect(reloaded.sent).toEqual(["Deploy"]);
    expect(reloaded.hook.result.current.messages).toMatchObject([{content: "Deploy", deliveryStatus: "sending"}]);
    await act(async () => {
      reloaded.answers[0].resolve({ok: true, queued: false});
      await retried;
    });
    expect(await retried).toBe(true);
    expect(reloaded.hook.result.current.messages).toEqual([]);
    expect(localStorage.getItem(storageKey)).toBeNull();
    expect(sent).toEqual(["Deploy"]);
  });

  test("a dismissed failure is gone, and not kept", async () => {
    localStorage.setItem(storageKey, JSON.stringify([{role: "user", content: "Old", clientId: "c1", deliveryStatus: "failed"}]));
    const {hook} = await mountDrafts();
    expect(hook.result.current.messages).toHaveLength(1);
    await act(async () => hook.result.current.dismissFailedMessage("c1"));
    expect(hook.result.current.messages).toEqual([]);
    expect(localStorage.getItem(storageKey)).toBeNull();
    expect(await hook.result.current.retryFailedMessage("c1")).toBe(false);
  });
});
