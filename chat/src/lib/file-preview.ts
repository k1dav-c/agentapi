// What the file viewer can preview: an HTML page (rendered in a sandboxed
// iframe), a Markdown document, or an image.
export type PreviewKind = "html" | "markdown" | "image";

const previewKinds: Record<string, PreviewKind> = {
  html: "html",
  htm: "html",
  md: "markdown",
  markdown: "markdown",
  mdown: "markdown",
  mkd: "markdown",
  mkdn: "markdown",
  png: "image",
  jpg: "image",
  jpeg: "image",
  gif: "image",
  webp: "image",
  bmp: "image",
  ico: "image",
  svg: "image",
};

// The preview the file at path has, by its extension.
export function previewKind(path: string): PreviewKind | undefined {
  const extension = path.toLowerCase().match(/\.([a-z0-9]+)$/)?.[1];
  return extension ? previewKinds[extension] : undefined;
}

// Whether ref is a path to a file, rather than a URL with a scheme
// (https:, mailto:, data:, ...), a protocol-relative URL or a fragment.
export function isRelativeRef(ref: string): boolean {
  return ref !== "" && !ref.startsWith("#") && !ref.startsWith("//") && !/^[a-z][a-z0-9+.-]*:/i.test(ref);
}

// Where a relative link or image in a document points: the file's path
// relative to the agent's working directory, and the line of a GitHub-style
// #L12 fragment. fromPath is the document's own path, relative to the same
// directory; a ref starting with "/" is from that directory. Undefined when
// ref isn't a relative path, or leads out of the working directory.
export function resolveRelativeRef(fromPath: string, ref: string): {path: string; line?: number} | undefined {
  if (!isRelativeRef(ref.trim())) return undefined;
  const [withoutFragment, fragment = ""] = ref.trim().split("#", 2);
  const target = withoutFragment.split("?", 1)[0];
  if (!target) return undefined;
  const segments = target.startsWith("/") ? [] : fromPath.split("/").slice(0, -1);
  for (const raw of target.split("/")) {
    let segment = raw;
    try {
      segment = decodeURIComponent(raw);
    } catch {
      // Not percent-encoded after all.
    }
    if (segment === "" || segment === ".") continue;
    if (segment === "..") {
      if (segments.length === 0) return undefined;
      segments.pop();
      continue;
    }
    segments.push(segment);
  }
  if (segments.length === 0) return undefined;
  const line = fragment.match(/^L(\d+)/)?.[1];
  return line ? {path: segments.join("/"), line: Number(line)} : {path: segments.join("/")};
}
