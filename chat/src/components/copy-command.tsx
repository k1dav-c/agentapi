"use client";

import {useState} from "react";
import {Check, Copy} from "lucide-react";

// CopyCommand is a shell command to copy and run, with its copy button.
export function CopyCommand({text}: {text: string}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="mt-2 flex items-stretch overflow-hidden rounded-lg border bg-term text-term-foreground">
      <code className="min-w-0 flex-1 overflow-x-auto px-3 py-2.5 font-mono text-[12.5px] leading-5 whitespace-pre [scrollbar-width:thin]">{text}</code>
      <button
        type="button"
        onClick={() => {
          void navigator.clipboard?.writeText(text).then(() => {
            setCopied(true);
            setTimeout(() => setCopied(false), 1600);
          });
        }}
        className="flex w-11 shrink-0 items-center justify-center border-l border-white/10 text-term-dim outline-none hover:text-term-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
        aria-label={copied ? "Copied" : "Copy the command"}
      >
        {copied ? <Check className="size-4 text-state-ready" /> : <Copy className="size-4" />}
      </button>
    </div>
  );
}
