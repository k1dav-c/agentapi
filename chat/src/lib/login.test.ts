import {describe, expect, test} from "bun:test";
import {isLoginNotice, loginNote, loginTitle, loginWhere, parseLoginRequired} from "./login";

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

describe("isLoginNotice", () => {
  const notice = "Claude Code is not logged in. Run `claude auth login` in a terminal on devbox as dave; AgentAPI picks up the login by itself.";

  test("the server's login error repeats the card", () => {
    expect(isLoginNotice(notice, claude)).toBe(true);
    expect(isLoginNotice("Claude Code: the turn failed (success): Not logged in · Please run /login", claude)).toBe(false);
  });

  test("a login notice replayed once the login is fixed is stale", () => {
    expect(isLoginNotice(notice, null)).toBe(true);
    expect(isLoginNotice("Codex is not logged in. Run `codex login` in a terminal; AgentAPI picks up the login by itself.", null)).toBe(true);
    // Another agent's login than the one the card shows.
    expect(isLoginNotice("Pi has no provider. Run `pi` in a terminal on devbox as dave; AgentAPI picks up the login by itself.", claude)).toBe(true);
  });

  test("other errors still show", () => {
    expect(isLoginNotice("Failed to start the agent: exec: \"claude\": executable file not found in $PATH", null)).toBe(false);
    expect(isLoginNotice("Dropped queued message: the agent exited", claude)).toBe(false);
  });
});
