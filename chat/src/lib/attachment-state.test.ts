import {describe, expect, test} from "bun:test";
import {
  parsePersistedAttachments,
  removeAttachmentToken,
} from "./attachment-state";

describe("attachment persistence", () => {
  test("restores only valid completed attachments", () => {
    const value = JSON.stringify([
      {id: "1", name: "report.txt", size: 42, filePath: "/tmp/report.txt"},
      {id: "2", name: "broken.txt"},
    ]);
    expect(parsePersistedAttachments(value)).toEqual([
      {id: "1", name: "report.txt", size: 42, filePath: "/tmp/report.txt"},
    ]);
  });

  test("handles malformed storage and removes its prompt token", () => {
    expect(parsePersistedAttachments("{")).toEqual([]);
    expect(removeAttachmentToken('Review @"./report.txt" please', "./report.txt"))
      .toBe("Review please");
  });
});
