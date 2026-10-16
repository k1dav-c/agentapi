import {describe, expect, test} from "bun:test";
import {isLoginNotice, loginNote, loginRetryOutcome, loginTitle, loginWhere, parseLoginRequired} from "./login";

const claude = {
  agent: "Claude Code",
  message: "Claude Code is not logged in.",
  command: "claude auth login",
  detail: "Uses your Claude subscription.",
  host: "devbox",
  user: "dave",
};

describe("parseLoginRequired", () => {
  test("keeps what the server reports", () => {
    expect(parseLoginRequired(claude)).toEqual(claude);
  });

  test("optional fields may be missing", () => {
    expect(parseLoginRequired({agent: "Pi", message: "No provider.", command: "pi"})).toEqual({
      agent: "Pi",
      message: "No provider.",
      command: "pi",
      detail: undefined,
      host: undefined,
      user: undefined,
    });
  });

  test("anything without a command is no login", () => {
    expect(parseLoginRequired(undefined)).toBeNull();
    expect(parseLoginRequired(null)).toBeNull();
    expect(parseLoginRequired("claude auth login")).toBeNull();
    expect(parseLoginRequired({agent: "Codex", command: ""})).toBeNull();
    expect(parseLoginRequired({agent: "Codex", command: 1})).toBeNull();
  });
});

describe("the login card's texts", () => {
  test("title names the agent", () => {
    expect(loginTitle(claude)).toBe("Claude Code needs you to log in");
    expect(loginTitle({...claude, agent: ""})).toBe("The agent needs you to log in");
  });

  test("where to run the command", () => {
    expect(loginWhere(claude)).toBe("on devbox as dave");
    expect(loginWhere({...claude, user: undefined})).toBe("on devbox");
    expect(loginWhere({...claude, host: undefined})).toBe("as dave");
    expect(loginWhere({...claude, host: undefined, user: undefined})).toBe("");
  });

  test("the note depends on whether AgentAPI retries by itself", () => {
    expect(loginNote(true)).toContain("notices the login by itself within a few seconds");
    expect(loginNote(false)).toContain("resumes this session");
  });
});

describe("loginRetryOutcome", () => {
  test("maps the response status", () => {
    expect(loginRetryOutcome(200)).toBe("checking");
    expect(loginRetryOutcome(409)).toBe("not-waiting");
    expect(loginRetryOutcome(400)).toBeNull();
    expect(loginRetryOutcome(500)).toBeNull();
  });
});

describe("isLoginNotice", () => {
  test("the server's login error repeats the card", () => {
    const notice = "Claude Code is not logged in. Run `claude auth login` in a terminal on devbox as dave; AgentAPI picks up the login by itself.";
    expect(isLoginNotice(notice, claude)).toBe(true);
    expect(isLoginNotice(notice, null)).toBe(false);
    expect(isLoginNotice("Claude Code: the turn failed (success): Not logged in · Please run /login", claude)).toBe(false);
  });
});
