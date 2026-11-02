"use client";

import {useEffect, useId, useState} from "react";
import {Check, Copy, Eye, EyeOff} from "lucide-react";
import {CopyCommand} from "@/components/copy-command";
import {copyText} from "@/lib/clipboard";
import {JOIN_SINCE, fetchJoinInfo, joinChoices, joinCommand, joinPrompt, type JoinInfo} from "@/lib/hub-join";

const selectClass =
  "h-10 w-full min-w-0 rounded-lg border bg-card px-3 font-mono text-[13px] shadow-xs outline-none transition-shadow focus-visible:border-ring focus-visible:ring-[3px] focus-visible:ring-ring/40";

const Code = ({children}: {children: React.ReactNode}) => <code className="font-mono text-[12px]">{children}</code>;

// CopyPrompt is a prompt to copy, in a box that scrolls, with its copy
// button; copy is what the button copies, the secret unmasked.
function CopyPrompt({text, copy}: {text: string; copy: string}) {
  const [copied, setCopied] = useState(false);
  return (
    <div className="relative mt-2 overflow-hidden rounded-lg border bg-muted/40">
      <div
        role="region"
        aria-label="Prompt"
        tabIndex={0}
        className="max-h-32 overflow-y-auto py-2.5 pr-12 pl-3 text-[12.5px] leading-5 whitespace-pre-wrap outline-none [overflow-wrap:anywhere] [scrollbar-width:thin] focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        {text}
      </div>
      <button
        type="button"
        onClick={() => {
          copyText(copy).then(
            () => {
              setCopied(true);
              setTimeout(() => setCopied(false), 1600);
            },
            () => {},
          );
        }}
        className="absolute top-1.5 right-2.5 flex size-8 items-center justify-center rounded-md border bg-background text-muted-foreground shadow-xs outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
        aria-label={copied ? "Copied" : "Copy the prompt"}
      >
        {copied ? <Check className="size-4 text-state-ready" /> : <Copy className="size-4" />}
      </button>
    </div>
  );
}

// JoinMachine shows how to join a machine to the hub, from where the hub
// says machines reach it (GET /api/join), which it asks each time it shows.
export function JoinMachine({root}: {root: string}) {
  const [info, setInfo] = useState<JoinInfo | "failed">();
  useEffect(() => {
    let active = true;
    fetchJoinInfo(root).then(
      (value) => {
        if (active) setInfo(value);
      },
      () => {
        if (active) setInfo("failed");
      },
    );
    return () => {
      active = false;
    };
  }, [root]);
  if (info === undefined) {
    return (
      <p role="status" className="text-sm text-muted-foreground">
        Asking the hub where machines reach it…
      </p>
    );
  }
  return <JoinSteps root={root} info={info} />;
}

// JoinSteps are the hub's URL on the network the machine shares with it,
// picked from those the hub found, whether the hub may update the
// machine's AgentAPIs, the command to run there, and a prompt for an agent
// there. The secret shows masked until asked for; the copy buttons copy it
// as it is.
export function JoinSteps({root, info}: {root: string; info: JoinInfo | "failed"}) {
  const [picked, setPicked] = useState(0);
  const [allowUpdate, setAllowUpdate] = useState(false);
  const [shown, setShown] = useState(false);
  const id = useId();
  const known = info === "failed" ? undefined : info;
  const choices = joinChoices(known, root);
  const hub = choices[Math.min(picked, choices.length - 1)];
  const options = {hub, others: choices.filter((choice) => choice !== hub), secret: known?.secret, allowUpdate};
  const onScreen = {...options, masked: !shown};
  const asIs = {...options, masked: false};
  const fromPage = "so this is the page's address: it works only without a sign-in page in front, unlike a Coder port URL.";
  const where =
    info === "failed"
      ? `The hub didn't say where machines reach it, ${fromPage}`
      : info.urls.length === 0
        ? `The hub found no address of its machine, ${fromPage}`
        : "Pick the address on the network the machine shares with the hub.";
  const secretNote =
    info === "failed"
      ? " Put in the hub's secret: the one given to agentapi hub serve."
      : info.secret_shown
        ? ""
        : " A hub without a token never shows its secret: put in the one given to agentapi hub serve.";
  return (
    <div className="space-y-4">
      <label className="block space-y-1.5">
        <span className="text-sm font-medium">Hub address</span>
        <select
          value={choices.indexOf(hub)}
          onChange={(event) => setPicked(Number(event.target.value))}
          aria-describedby={`${id}-where`}
          className={selectClass}
        >
          {choices.map((choice, i) => (
            <option key={choice.url} value={i}>
              {choice.interface ? `${choice.url} · ${choice.interface}` : choice.url}
            </option>
          ))}
        </select>
        <span id={`${id}-where`} className="block text-xs text-muted-foreground">
          {where}
        </span>
      </label>
      <label className="flex items-start gap-2.5">
        <input type="checkbox" checked={allowUpdate} onChange={(event) => setAllowUpdate(event.target.checked)} className="mt-0.5 size-4 shrink-0 accent-primary" />
        <span>
          <span className="block text-sm font-medium">Allow updates from the hub</span>
          <span className="block text-xs text-muted-foreground">
            <Code>--allow-update</Code>: the hub may update the machine&apos;s AgentAPIs to the latest release.
          </span>
        </span>
      </label>
      <div>
        <div className="flex items-center justify-between gap-3">
          <p className="text-sm font-medium">Run on the machine</p>
          {options.secret !== undefined && (
            <button
              type="button"
              onClick={() => setShown((value) => !value)}
              className="flex items-center gap-1.5 rounded text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/50"
            >
              {shown ? <EyeOff className="size-3.5" aria-hidden="true" /> : <Eye className="size-3.5" aria-hidden="true" />}
              {shown ? "Hide the secret" : "Show the secret"}
            </button>
          )}
        </div>
        <CopyCommand text={joinCommand(onScreen)} copy={joinCommand(asIs)} wrap />
        <p className="mt-1.5 text-xs text-muted-foreground">
          <Code>{"{ip}"}</Code> is the machine&apos;s address on the route to the hub: its AgentAPIs look it up again while they run, so the hub follows the machine
          when it gets another address. It needs AgentAPI {JOIN_SINCE} or later there: <Code>sudo agentapi update</Code>.{secretNote}
        </p>
      </div>
      <div>
        <p className="text-sm font-medium">Prompt for an agent on that machine</p>
        <p className="text-xs text-muted-foreground">Or paste this to the agent there: it checks the hub, joins and reports, and restarts nothing.</p>
        <CopyPrompt text={joinPrompt(onScreen)} copy={joinPrompt(asIs)} />
      </div>
    </div>
  );
}
