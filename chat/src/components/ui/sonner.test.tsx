import {describe, expect, test} from "bun:test";
import type {ToastT} from "sonner";
import {clearToasts} from "./sonner";

describe("clearToasts", () => {
  test("each toast is dismissed as its close button would, then all go", () => {
    const closed: string[] = [];
    const toasts = [
      {id: 1, onDismiss: () => void closed.push("first")},
      {id: 2},
      {id: 3, onDismiss: (shown: ToastT) => void closed.push(`third ${shown.id}`)},
    ];
    let dismissed = 0;
    clearToasts(toasts, () => void dismissed++);
    expect(closed).toEqual(["first", "third 3"]);
    expect(dismissed).toBe(1);
  });
});
