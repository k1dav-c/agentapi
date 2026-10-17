import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {ShieldQuestion} from "lucide-react";
import {CardStatus, DecisionCardShell} from "./decision-card";

// The ids an attribute refers to, and whether each names an element.
function referenced(html: string, attribute: string): {ids: string[]; found: boolean} {
  const value = html.match(new RegExp(`${attribute}="([^"]*)"`))?.[1] ?? "";
  const ids = value.split(" ").filter(Boolean);
  return {ids, found: ids.length > 0 && ids.every((id) => html.includes(`id="${id}"`))};
}

describe("DecisionCardShell", () => {
  test("names the card by its kind and title", () => {
    const html = renderToStaticMarkup(
      <DecisionCardShell icon={ShieldQuestion} label="Approval" title="Run make release" data-request-kind="approval">
        <p>options</p>
      </DecisionCardShell>,
    );
    const label = referenced(html, "aria-labelledby");
    expect(label.ids).toHaveLength(2);
    expect(label.found).toBe(true);
    expect(html).toContain('data-request-kind="approval"');
    expect(html).toContain(">Approval</span>");
    expect(html).toContain("Run make release</h2>");
  });

  test("without a kind it is named by the title", () => {
    const html = renderToStaticMarkup(<DecisionCardShell title="Do you want to proceed?"><p /></DecisionCardShell>);
    const label = referenced(html, "aria-labelledby");
    expect(label.ids).toHaveLength(1);
    expect(label.found).toBe(true);
  });
});

describe("CardStatus", () => {
  test("the status region is there before it has anything to say", () => {
    const html = renderToStaticMarkup(<CardStatus className="px-3.5" status="" />);
    expect(html).toContain('<p role="status" class="sr-only"></p>');
    expect(html).toContain('<p role="alert" class="sr-only"></p>');
  });

  test("shows the status, or the error instead", () => {
    expect(renderToStaticMarkup(<CardStatus className="px-3.5" status="Sending your answer…" />)).toContain(
      '<p role="status" class="px-3.5 text-muted-foreground">Sending your answer…</p>',
    );
    const failed = renderToStaticMarkup(<CardStatus className="px-3.5" status="Run it, then check again." error="Not signed in" />);
    expect(failed).toContain('<p role="status" class="sr-only">Run it, then check again.</p>');
    expect(failed).toContain('<p role="alert" class="px-3.5 text-destructive">Not signed in</p>');
  });
});
