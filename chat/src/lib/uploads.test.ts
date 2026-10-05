import {describe, expect, test} from "bun:test";
import {attachedPaths, filesToUploadFromPaste, isImageFile, pastedFileName, uploadedImageURL, withoutUploadedImages} from "./uploads";

describe("uploadedImageURL", () => {
  const base = "https://host/proxy";
  test("an uploaded image", () => {
    expect(uploadedImageURL(base, "/tmp/agentapi-uploads-1074021518/0123456789abcdef/screen shot.png"))
      .toBe("https://host/proxy/uploads/0123456789abcdef/screen%20shot.png");
  });
  test("not an image, or not one of ours", () => {
    expect(uploadedImageURL(base, "/tmp/agentapi-uploads-1/0123456789abcdef/notes.txt")).toBeUndefined();
    expect(uploadedImageURL(base, "/home/me/picture.png")).toBeUndefined();
    expect(uploadedImageURL(base, "/tmp/agentapi-uploads-1/not-a-checksum/a.png")).toBeUndefined();
  });
});

describe("attachedPaths", () => {
  test("finds every @\"...\" reference", () => {
    expect(attachedPaths('Look at this @"/tmp/a b.png" and @"/tmp/c.txt"')).toEqual(["/tmp/a b.png", "/tmp/c.txt"]);
    expect(attachedPaths("no files @here")).toEqual([]);
  });
});

describe("pasted files", () => {
  test("screenshots get a dated name", () => {
    const now = new Date(2026, 9, 5, 5, 7, 9);
    expect(pastedFileName("image.png", "image/png", now)).toBe("pasted-20261005-050709.png");
    expect(pastedFileName("", "image/jpeg", now)).toBe("pasted-20261005-050709.jpg");
    expect(pastedFileName("diagram.png", "image/png", now)).toBe("diagram.png");
  });
  test("text on the clipboard wins over an image of it", () => {
    const file = new File(["x"], "image.png", {type: "image/png"});
    expect(filesToUploadFromPaste({getData: () => "", files: [file]})).toEqual([file]);
    expect(filesToUploadFromPaste({getData: () => "A1\tB1", files: [file]})).toEqual([]);
  });
  test("image files", () => {
    expect(isImageFile({name: "a.PNG"})).toBe(true);
    expect(isImageFile({name: "blob", type: "image/webp"})).toBe(true);
    expect(isImageFile({name: "a.txt", type: "text/plain"})).toBe(false);
  });
});

describe("withoutUploadedImages", () => {
  test("drops uploaded image references, keeps everything else", () => {
    const image = '@"/tmp/agentapi-uploads-1/0123456789abcdef/pasted-1.png"';
    expect(withoutUploadedImages(`What color is this? ${image}`)).toBe("What color is this?");
    expect(withoutUploadedImages(image)).toBe("");
    expect(withoutUploadedImages('Read @"/tmp/agentapi-uploads-1/0123456789abcdef/notes.txt" please'))
      .toBe('Read @"/tmp/agentapi-uploads-1/0123456789abcdef/notes.txt" please');
    expect(withoutUploadedImages('See @"/home/me/a.png"')).toBe('See @"/home/me/a.png"');
  });
});
