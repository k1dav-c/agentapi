import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {VersionTag} from "./version-tag";

describe("VersionTag", () => {
  test("an older version than the hub's is marked, and says so", () => {
    const html = renderToStaticMarkup(<VersionTag version="1.6.1" hubVersion="2.1.0" />);
    expect(html).toContain(">v1.6.1</span>");
    expect(html).toContain(">older</span>");
    expect(html).toContain('title="AgentAPI v1.6.1, older than the hub (v2.1.0)"');
    expect(html).toContain('<span class="sr-only">AgentAPI v1.6.1, older than the hub (v2.1.0)</span>');
  });

  test("the hub's version, a newer one or a development build isn't", () => {
    for (const [version, hub] of [["2.1.0", "2.1.0"], ["2.2.0", "2.1.0"], ["0.0.0-dev", "2.1.0"], ["1.6.1", "0.0.0-dev"]]) {
      expect(renderToStaticMarkup(<VersionTag version={version} hubVersion={hub} />)).not.toContain(">older</span>");
    }
  });

  test("the last known version is dimmed and not marked", () => {
    const html = renderToStaticMarkup(<VersionTag version="1.6.1" hubVersion="2.1.0" lastKnown />);
    expect(html).toContain("opacity-55");
    expect(html).not.toContain(">older</span>");
    expect(html).toContain('title="Last known: AgentAPI v1.6.1"');
  });
});
