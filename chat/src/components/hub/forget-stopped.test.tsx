import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import {hubInstance} from "@/lib/hub-fixtures";
import {forgetStoppedTitle, StoppedAgents} from "./dialogs";
import {ForgetStoppedRow} from "./hub-list";

const text = (html: string) =>
  html
    .replace(/<[^>]+>/g, " ")
    .replace(/\s+/g, " ")
    .trim();

describe("forget all stopped", () => {
  test("the dialog asks for the number of them", () => {
    expect(forgetStoppedTitle(3)).toBe("Forget 3 stopped agents?");
    expect(forgetStoppedTitle(1)).toBe("Forget 1 stopped agent?");
  });

  test("the dialog lists each one's name and machine", () => {
    const html = renderToStaticMarkup(
      <StoppedAgents
        instances={[
          hubInstance("dev-1:3284", {name: "checkout-api", hostname: "dev-1", state: "stopped"}),
          hubInstance("ci-7:3284", {state: "stopped"}),
        ]}
      />,
    );
    expect(html).toContain('aria-label="Stopped agents"');
    expect(html).toContain("overflow-y-auto");
    expect(text(html)).toBe("checkout-api dev-1 ci-7:3284");
  });

  test("the list's row says how many stopped, and forgets them all", () => {
    expect(text(renderToStaticMarkup(<ForgetStoppedRow count={4} onForget={() => {}} />))).toBe("4 stopped · Forget all");
  });
});
