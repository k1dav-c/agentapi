import {describe, expect, test} from "bun:test";
import {formatBytes, formatSize} from "./format-size";

describe("formatSize", () => {
  test("bytes, then KB, MB and GB with one decimal", () => {
    expect(formatSize(0)).toBe("0 B");
    expect(formatSize(1023)).toBe("1023 B");
    expect(formatSize(1024)).toBe("1.0 KB");
    expect(formatSize(12_390)).toBe("12.1 KB");
    expect(formatSize(3.4 * 1024 * 1024)).toBe("3.4 MB");
    expect(formatSize(2 * 1024 ** 3)).toBe("2.0 GB");
    expect(formatSize(2048 * 1024 ** 3)).toBe("2048.0 GB");
  });
});

describe("formatBytes", () => {
  test("the exact size, with the short one from a KB on", () => {
    expect(formatBytes(1)).toBe("1 byte");
    expect(formatBytes(512)).toBe("512 bytes");
    expect(formatBytes(12_390)).toBe("12,390 bytes (12.1 KB)");
  });
});
