// Linking bare URLs in the chat's Markdown (ProcessedMessage,
// MarkdownDocument): a transform of the mdast tree remark-gfm parsed.

import {LinkifyIt} from "linkify-it";

// The part of an mdast node the plugin reads and writes.
export type MarkdownNode = {
  type: string;
  value?: string;
  url?: string;
  children?: MarkdownNode[];
};

// Finds the links in text: bare URLs, www. addresses and emails.
export const linkify = new LinkifyIt();

// isAutolink says whether a link is a bare URL GFM linked: its text is its
// URL, "http://" before a www. address or "mailto:" before an email.
function isAutolink(url: string | undefined, text: string): boolean {
  return url === text || url === `http://${text}` || url === `mailto:${text}`;
}

// remarkLinkify is a remark plugin that links the bare URLs in text, as
// linkify-it finds them, and stops a link at the Chinese punctuation after
// it (，。！？；：), which GFM's autolinks run on into; links and code stay as
// they are.
export function remarkLinkify() {
  return (tree: MarkdownNode) => {
    const visit = (node: MarkdownNode) => {
      if (!node.children || ["link", "code", "inlineCode"].includes(node.type)) {
        return;
      }

      node.children = node.children.flatMap((child) => {
        if (
          child.type === "link" &&
          child.children?.length === 1 &&
          child.children[0].type === "text" &&
          child.children[0].value
        ) {
          const text = child.children[0].value;
          // Only a bare URL GFM linked runs on into the Chinese punctuation
          // after it; a [label](url) link keeps its label and its URL.
          if (!isAutolink(child.url, text)) return child;
          const punctuationIndex = text.search(/[，。！？；：]/u);
          if (punctuationIndex !== -1) {
            const linkText = text.slice(0, punctuationIndex);
            return [
              {
                ...child,
                // The URL less what ran on, as GFM wrote it (with http://
                // before www.).
                url: child.url!.slice(0, child.url!.length - (text.length - linkText.length)),
                children: [{type: "text", value: linkText}],
              },
              {type: "text", value: text.slice(punctuationIndex)},
            ];
          }
          return child;
        }

        if (child.type !== "text" || !child.value) {
          visit(child);
          return child;
        }

        const matches = linkify.match(child.value);
        if (!matches) return child;

        const parts: MarkdownNode[] = [];
        let cursor = 0;
        for (const match of matches) {
          const punctuationIndex = match.text.search(/[，。！？；：]/u);
          const linkText =
            punctuationIndex === -1
              ? match.text
              : match.text.slice(0, punctuationIndex);
          const normalizedUrl =
            linkify.match(linkText)?.[0]?.url ?? linkText;
          const lastIndex = match.index + linkText.length;

          if (match.index > cursor) {
            parts.push({
              type: "text",
              value: child.value.slice(cursor, match.index),
            });
          }
          parts.push({
            type: "link",
            url: normalizedUrl,
            children: [{type: "text", value: linkText}],
          });
          cursor = lastIndex;
        }
        if (cursor < child.value.length) {
          parts.push({type: "text", value: child.value.slice(cursor)});
        }
        return parts;
      });
    };

    visit(tree);
  };
}
