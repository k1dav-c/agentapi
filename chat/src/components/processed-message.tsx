import React from "react";
import {LinkifyIt} from "linkify-it";
import ReactMarkdown from "react-markdown";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";

type MarkdownNode = {
  type: string;
  value?: string;
  url?: string;
  children?: MarkdownNode[];
};

const linkify = new LinkifyIt();

function remarkLinkify() {
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
          const punctuationIndex = text.search(/[，。！？；：]/u);
          if (punctuationIndex !== -1) {
            const linkText = text.slice(0, punctuationIndex);
            return [
              {
                ...child,
                url: linkify.match(linkText)?.[0]?.url ?? linkText,
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

interface ProcessedMessageProps {
  messageContent: string;
  isUser: boolean;
  renderMode?: "raw" | "markdown";
  searchQuery?: string;
}

function linkifyTerminalText(content: string) {
  const matches = linkify.match(content);
  if (!matches) return content;

  const parts: React.ReactNode[] = [];
  let cursor = 0;

  for (const match of matches) {
    if (match.index > cursor) {
      parts.push(content.slice(cursor, match.index));
    }
    parts.push(
      <a
        key={`${match.index}-${match.lastIndex}`}
        href={match.url}
        target="_blank"
        rel="noopener noreferrer"
        className="underline decoration-current/30 underline-offset-4 transition hover:decoration-current"
      >
        {match.text}
      </a>,
    );
    cursor = match.lastIndex;
  }

  if (cursor < content.length) {
    parts.push(content.slice(cursor));
  }
  return parts;
}

export const ProcessedMessage = React.memo(function ProcessedMessage({
  messageContent,
  isUser,
  renderMode = isUser ? "markdown" : "raw",
  searchQuery = "",
}: ProcessedMessageProps) {
  if (renderMode === "markdown" && searchQuery.trim()) {
    return (
      <div className="min-w-0 whitespace-pre-wrap text-left text-sm leading-6">
        {highlightTerminalText(messageContent, searchQuery)}
      </div>
    );
  }

  if (renderMode === "raw") {
    return (
      <div className="min-w-0 whitespace-pre-wrap break-words text-left font-mono text-[13px] leading-5 [overflow-wrap:anywhere] [tab-size:4]">
        {searchQuery
          ? highlightTerminalText(messageContent, searchQuery)
          : linkifyTerminalText(messageContent)}
      </div>
    );
  }

  return (
    <div className="min-w-0 text-left text-sm leading-6">
      <ReactMarkdown
        remarkPlugins={[remarkLinkify, remarkGfm, remarkBreaks]}
        components={{
          a: (properties) => {
            const {children, ...anchorProps} = properties;
            delete anchorProps.node;
            return (
              <a
                {...anchorProps}
                target="_blank"
                rel="noopener noreferrer"
                className="break-words underline decoration-current/30 underline-offset-4 transition [overflow-wrap:anywhere] hover:decoration-current focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2"
              >
                {children}
              </a>
            );
          },
          p: ({children}) => <p className="my-2 first:mt-0 last:mb-0">{children}</p>,
          blockquote: ({children}) => (
            <blockquote className="my-3 rounded-r-lg border-l-4 border-primary/70 bg-muted/60 px-4 py-2 text-foreground shadow-sm [&>p]:my-1">
              {children}
            </blockquote>
          ),
          ul: ({children}) => (
            <ul className="my-3 list-outside list-disc space-y-1 pl-6 marker:text-primary">
              {children}
            </ul>
          ),
          ol: ({children}) => (
            <ol className="my-3 list-outside list-decimal space-y-1 pl-6 marker:font-semibold marker:text-primary">
              {children}
            </ol>
          ),
          li: ({children}) => (
            <li className="pl-1 [&>p]:my-0">{children}</li>
          ),
          code: ({children, className}) => (
            <code
              className={`${className ?? ""} rounded border border-border bg-muted px-1.5 py-0.5 font-mono text-[0.9em] font-medium text-foreground`}
            >
              {children}
            </code>
          ),
          table: ({children}) => (
            <div className="my-3 max-w-full overflow-x-auto rounded-lg border">
              <table className="w-full border-collapse text-left text-sm">
                {children}
              </table>
            </div>
          ),
          thead: ({children}) => (
            <thead className="bg-muted/70">{children}</thead>
          ),
          th: ({children}) => (
            <th className="border-b border-r px-3 py-2 font-semibold last:border-r-0">
              {children}
            </th>
          ),
          td: ({children}) => (
            <td className="border-b border-r px-3 py-2 align-top last:border-r-0">
              {children}
            </td>
          ),
          tr: ({children}) => (
            <tr className="last:[&>td]:border-b-0">{children}</tr>
          ),
          pre: ({children}) => (
            <pre className="my-3 max-w-full overflow-x-auto rounded-lg border border-zinc-700 bg-zinc-950 p-4 text-zinc-100 shadow-inner whitespace-pre [&>code]:border-0 [&>code]:bg-transparent [&>code]:p-0 [&>code]:font-normal [&>code]:text-inherit">
              {children}
            </pre>
          ),
        }}
      >
        {messageContent}
      </ReactMarkdown>
    </div>
  );
});

function highlightTerminalText(content: string, query: string) {
  const normalizedQuery = query.trim();
  if (!normalizedQuery) return linkifyTerminalText(content);

  const expression = new RegExp(
    `(${normalizedQuery.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")})`,
    "giu",
  );
  return content.split(expression).map((part, index) =>
    part.toLocaleLowerCase() === normalizedQuery.toLocaleLowerCase() ? (
      <mark
        key={`${index}-${part}`}
        className="rounded-sm bg-amber-300 px-0.5 text-black"
      >
        {part}
      </mark>
    ) : (
      <React.Fragment key={`${index}-${part}`}>
        {linkifyTerminalText(part)}
      </React.Fragment>
    ),
  );
}
