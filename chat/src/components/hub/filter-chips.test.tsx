import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {AgentChips, StatusChips} from "./filter-chips";

// Each chip as its label, count and whether it's on.
const chips = (html: string) =>
  [...html.matchAll(/<button[^>]*aria-pressed="(true|false)"[^>]*>(.*?)<\/button>/g)].map(([, pressed, inner]) => [
    inner
      .replace(/<[^>]+>/g, " ")
      .replace(/\s+/g, " ")
      .trim(),
    pressed === "true",
  ]);

describe("StatusChips", () => {
  test("a chip a state with its count, the one picked pressed", () => {
    const html = renderToStaticMarkup(
      <StatusChips filter="working" counts={{all: 5, "needs-you": 1, working: 2, unread: 0, offline: 2}} onChange={() => {}} />,
    );
    expect(html).toContain('role="group" aria-label="Filter the agents"');
    expect(chips(html)).toEqual([
      ["All 5", false],
      ["Need you 1", false],
      ["Working 2", true],
      ["Unseen 0", false],
      ["Offline 2", false],
    ]);
  });
});

describe("AgentChips", () => {
  const counts = {claude: 3, codex: 1, pi: 0, other: 2};

  test("Any agent, then a chip with its mark for each agent the hub has", () => {
    const html = renderToStaticMarkup(<AgentChips agents={["claude", "codex", "other"]} picked={new Set()} counts={counts} onChange={() => {}} />);
    expect(html).toContain('role="group" aria-label="Filter by agent"');
    expect(chips(html)).toEqual([
      ["Any agent 6", true],
      ["Claude Code 3", false],
      ["Codex 1", false],
      ["Other 2", false],
    ]);
    expect(html).toContain('data-agent-mark="claude"');
    expect(html).toContain('data-agent-mark="codex"');
    expect(html).not.toContain('data-agent-mark="pi"');
  });

  test("the agents picked are pressed, and Any agent isn't", () => {
    const html = renderToStaticMarkup(<AgentChips agents={["claude", "codex", "pi"]} picked={new Set(["codex", "pi"])} counts={counts} onChange={() => {}} />);
    expect(chips(html)).toEqual([
      ["Any agent 4", false],
      ["Claude Code 3", false],
      ["Codex 1", true],
      ["Pi 0", true],
    ]);
  });
});
