import {describe, expect, test} from "bun:test";
import {renderToStaticMarkup} from "react-dom/server";
import type {InstanceState} from "@/lib/hub";
import {StatusGlyph, stateTone} from "./status-glyph";

const states: InstanceState[] = ["needs-you", "working", "unread", "idle", "muted", "connecting", "unreachable", "stopped", "unknown"];

describe("StatusGlyph", () => {
  test("every state has a shape and a color, the unknown one too", () => {
    for (const state of states) {
      expect(renderToStaticMarkup(<StatusGlyph state={state} />)).toStartWith("<svg");
      expect(stateTone[state]).toStartWith("text-");
    }
  });
});
