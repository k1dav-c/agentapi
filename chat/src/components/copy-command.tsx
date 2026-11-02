"use client";

import {useState} from "react";
import {Check, Copy} from "lucide-react";
import {copyText} from "@/lib/clipboard";
import {cn} from "@/lib/utils";

// CopyCommand is a shell command to copy and run, with its copy button;
// wrap shows a long one whole, such as one to run with sudo. copy is what
// the button copies when it differs from what shows, such as a command
// whose secret is masked on screen.
export function CopyCommand({text, copy = text, wrap = false}: {text: string; copy?: string; wrap?: boolean}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-2 flex items-stretch overflow-hidden rounded-lg border bg-term text-term-foreground">
      <code
        className={cn(
          "min-w-0 flex-1 px-3 py-2.5 font-mono text-[12.5px] leading-5",
          wrap ? "whitespace-pre-wrap [overflow-wrap:anywhere]" : "overflow-x-auto whitespace-pre [scrollbar-width:thin]",
        )}
      >
        {text}
      </code>
      <button
        type="button"
        onClick={() => {
          // copyText works on a plain-http page too, as a hub on a VPN is.
          copyText(copy).then(
            () => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1600);
            },
            () => {},
          );
        }}
        className="flex w-11 shrink-0 items-center justify-center border-l border-white/10 text-term-dim outline-none hover:text-term-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
        aria-label={copied ? "Copied" : "Copy the command"}
      >
        {copied ? <Check className="size-4 text-state-ready" /> : <Copy className="size-4" />}
      </button>
    </div>
  );
}
