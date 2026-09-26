import {describe, expect, test} from "bun:test";
import {currentPromptQuestion, parseInteractiveToolInput} from "./interactive-tool";

const questions = [
  {question: "Who runs it?", options: [{label: "Just me"}, {label: "A team"}]},
];

describe("parseInteractiveToolInput", () => {
  test("parses Claude AskUserQuestion input", () => {
    expect(parseInteractiveToolInput("AskUserQuestion", {questions})).toEqual({questions});
  });

  test("parses Codex request_user_input JSON string input", () => {
    expect(parseInteractiveToolInput("request_user_input", JSON.stringify({questions}))).toEqual({questions});
  });

  test("ExitPlanMode is interactive without options", () => {
    expect(parseInteractiveToolInput("ExitPlanMode", {plan: "x"})).toEqual({questions: []});
  });

  test("other tools and malformed input are not interactive", () => {
    expect(parseInteractiveToolInput("exec", {questions})).toBeNull();
    expect(parseInteractiveToolInput("request_user_input", "{not json")).toBeNull();
  });
});

describe("currentPromptQuestion", () => {
  test("reads Codex's question header", () => {
    expect(currentPromptQuestion("  Question 2/3 (2 unanswered)\n  Who runs it?")).toBe(1);
  });

  test("uses the latest header", () => {
    expect(currentPromptQuestion("Question 1/3\n...\nQuestion 3/3")).toBe(2);
  });

  test("returns null without a header", () => {
    expect(currentPromptQuestion("❯ 1. Yes\n  2. No")).toBeNull();
  });
});
