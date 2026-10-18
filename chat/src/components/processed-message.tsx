import React from "react";
import {LinkifyIt} from "linkify-it";
import {Check, Clipboard} from "lucide-react";
import ReactMarkdown, {type Components} from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import remarkBreaks from "remark-breaks";
import remarkGfm from "remark-gfm";
import {common} from "@/lib/highlight-langs";
import {copyText} from "@/lib/clipboard";
import {parseFilePathRef} from "@/lib/file-refs";
import {FilePathCode, inlineCodeClass} from "./file-path";

type MarkdownNode = {
  type: string;
  value?: string;
  url?: string;
  children?: MarkdownNode[];
};

const linkify = new LinkifyIt();

// Set inside a link, where code that names a file stays plain: a click
// would open the file and follow the link, and a control can't sit in a
// link.
const InsideLink = React.createContext(false);

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
  // Inline code that names a file opens it in the file viewer (Markdown
  // mode; for the agent's own messages).
  linkFilePaths?: boolean;
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

function CodeBlockCopy({ children }: { children: React.ReactNode }) {
  const [copied, setCopied] = React.useState(false);

  const getCodeString = (node: React.ReactNode): string => {
    if (typeof node === "string") return node;
    if (Array.isArray(node)) return node.map(getCodeString).join("");
    if (React.isValidElement(node) && node.props) {
      return getCodeString((node.props as { children?: React.ReactNode }).children ?? "");
    }
    return "";
  };

  const handleCopy = async () => {
    try {
      await copyText(getCodeString(children));
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1600);
    } catch {
      // Clipboard access may be blocked
    }
  };

  return (
    <div className="group/code relative my-3">
      <pre className="max-w-full overflow-x-auto rounded-lg border border-code-block-border bg-code-block-bg p-4 text-code-block-text shadow-inner whitespace-pre [&>code]:border-0 [&>code]:bg-transparent [&>code]:p-0 [&>code]:font-normal [&>code]:text-inherit">
        {children}
      </pre>
      <button
        type="button"
        onClick={() => void handleCopy()}
        className="absolute right-2 top-2 grid size-8 place-items-center rounded-md border border-code-block-border bg-code-block-bg text-code-block-text opacity-0 transition hover:bg-code-block-border focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-hover/code:opacity-100"
        aria-label={copied ? "Copied" : "Copy code"}
      >
        {copied ? <Check className="size-3.5" /> : <Clipboard className="size-3.5" />}
      </button>
    </div>
  );
}

export const ProcessedMessage = React.memo(function ProcessedMessage({
  messageContent,
  isUser,
  renderMode = isUser ? "markdown" : "raw",
  searchQuery = "",
  linkFilePaths = false,
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
    <div className="prose-transcript min-w-0 text-left text-[15px] leading-[1.7]">
      <ReactMarkdown
        remarkPlugins={[remarkLinkify, remarkGfm, remarkBreaks]}
        rehypePlugins={[[rehypeHighlight, {languages: common}]]}
        components={linkFilePaths ? linkingComponents : plainComponents}
      >
        {messageContent}
      </ReactMarkdown>
    </div>
  );
});

// How Markdown elements render in the chat. With linkFilePaths, inline code
// that names a file opens it in the file viewer.
function markdownComponents(linkFilePaths: boolean): Components {
  return {
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
          <InsideLink.Provider value={true}>{children}</InsideLink.Provider>
        </a>
      );
    },
    p: ({children}) => <p className="my-2.5 first:mt-0 last:mb-0">{children}</p>,
    blockquote: ({children}) => (
      <blockquote className="my-3 border-l-2 border-border pl-4 text-muted-foreground [&>p]:my-1">
        {children}
      </blockquote>
    ),
    ul: ({children}) => (
      <ul className="my-3 list-outside list-disc space-y-1 pl-6 marker:text-muted-foreground">
        {children}
      </ul>
    ),
    ol: ({children}) => (
      <ol className="my-3 list-outside list-decimal space-y-1 pl-6 marker:font-mono marker:text-[0.8em] marker:text-muted-foreground">
        {children}
      </ol>
    ),
    li: ({children}) => (
      <li className="pl-1 [&>p]:my-0">{children}</li>
    ),
    code: ({children, className}) => (
      <InlineCode className={className} linkFilePaths={linkFilePaths}>
        {children}
      </InlineCode>
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
    pre: ({children}) => <CodeBlockCopy>{children}</CodeBlockCopy>,
  };
}

const plainComponents = markdownComponents(false);
const linkingComponents = markdownComponents(true);

const headingClass = "font-semibold leading-tight first:mt-0";

// A Markdown file, as a document: lines that wrap in the source aren't
// broken, headings stand out, and relative images and links are resolved
// from the file. imageURL returns where a relative image loads from;
// openLink opens a relative link (in the file viewer), or returns false to
// leave it to the browser.
export const MarkdownDocument = React.memo(function MarkdownDocument({
  content,
  imageURL,
  openLink,
}: {
  content: string;
  imageURL: (ref: string) => string | undefined;
  openLink: (ref: string) => boolean;
}) {
  const components = React.useMemo<Components>(
    () => ({
      ...linkingComponents,
      h1: ({children}) => <h1 className={`mb-3 mt-7 border-b pb-2 text-2xl ${headingClass}`}>{children}</h1>,
      h2: ({children}) => <h2 className={`mb-3 mt-7 border-b pb-1.5 text-xl ${headingClass}`}>{children}</h2>,
      h3: ({children}) => <h3 className={`mb-2 mt-6 text-lg ${headingClass}`}>{children}</h3>,
      h4: ({children}) => <h4 className={`mb-2 mt-5 text-base ${headingClass}`}>{children}</h4>,
      h5: ({children}) => <h5 className={`mb-2 mt-5 text-sm ${headingClass}`}>{children}</h5>,
      h6: ({children}) => <h6 className={`mb-2 mt-5 text-sm text-muted-foreground ${headingClass}`}>{children}</h6>,
      hr: () => <hr className="my-6 border-border" />,
      img: ({src, alt, title}) => {
        const url = typeof src === "string" ? (imageURL(src) ?? src) : undefined;
        // eslint-disable-next-line @next/next/no-img-element -- served by AgentAPI, not optimizable
        return <img src={url} alt={alt ?? ""} title={title} loading="lazy" className="inline-block max-w-full rounded-sm align-middle" />;
      },
      a: ({children, href, title}) => (
        <a
          href={href}
          title={title}
          target="_blank"
          rel="noopener noreferrer"
          onClick={(event) => {
            if (href && openLink(href)) event.preventDefault();
          }}
          className="break-words text-primary underline decoration-current/30 underline-offset-4 transition [overflow-wrap:anywhere] hover:decoration-current focus-visible:rounded-sm focus-visible:outline-2 focus-visible:outline-offset-2"
        >
          <InsideLink.Provider value={true}>{children}</InsideLink.Provider>
        </a>
      ),
    }),
    [imageURL, openLink],
  );
  return (
    <div className="prose-transcript min-w-0 text-left text-[15px] leading-[1.7]">
      <ReactMarkdown
        remarkPlugins={[remarkLinkify, remarkGfm]}
        rehypePlugins={[[rehypeHighlight, {languages: common}]]}
        components={components}
      >
        {content}
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
        className="rounded-sm bg-search-highlight px-0.5 text-search-highlight-text"
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

// Inline or fenced code. Inline code that names a file opens it, unless it
// sits in a link.
function InlineCode({children, className, linkFilePaths}: {children?: React.ReactNode; className?: string; linkFilePaths?: boolean}) {
  const insideLink = React.useContext(InsideLink);
  // Only inline code: a fenced block has a language class or ends in a
  // newline, which no path has.
  const fileRef = linkFilePaths && !insideLink && !className && typeof children === "string" ? parseFilePathRef(children) : null;
  if (fileRef) return <FilePathCode text={children as string} fileRef={fileRef} />;
  return <code className={`${className ?? ""} ${inlineCodeClass}`}>{children}</code>;
}
