"use client";

import {useState} from "react";
import {RefreshCw, RotateCcw, Unplug} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import type {GitHubStatus} from "@/lib/hub-dispatch";
import type {ReposLoad} from "@/lib/hub-github-store";
import type {Load} from "@/lib/load";
import {cn} from "@/lib/utils";
import {ActionRow, AppCard, AppSkeleton, ConnectionPill} from "./github-app-card";
import {DisconnectDialog, ReconnectDialog} from "./github-dialogs";
import {ConnectGitHub} from "./issues/setup";
import {ReadError} from "./read-error";
import {useHubGitHub} from "./use-hub-github";

// GitHubSettings is the GitHub section of the hub's settings: the hub's
// GitHub App, where it is installed and on which repositories, and the
// ways to change that on GitHub, connect another app, or disconnect.
// activeJobs is how many jobs at work push with the app's tokens. What it
// reads, and a disconnect, is the Issues page's too (useHubGitHub).
export function GitHubSettings({root, activeJobs = 0, headingRef}: {root: string; activeJobs?: number; headingRef?: React.Ref<HTMLHeadingElement>}) {
  const github = useHubGitHub(root);
  const shown = github.status.state === "ready";
  return (
    <GitHubSettingsView
      root={root}
      status={github.status}
      repos={github.repos}
      reading={github.reading}
      activeJobs={activeJobs}
      headingRef={headingRef}
      onRetry={async () => {
        // What it read stays: a toast says this read failed.
        const why = await github.read();
        if (why && shown) toast.error("Couldn't read the hub's GitHub setup again", {description: why});
      }}
      onDisconnect={github.disconnect}
    />
  );
}

// GitHubSettingsView draws the section from what was read.
export function GitHubSettingsView({
  root,
  status,
  repos,
  reading,
  activeJobs,
  headingRef,
  onRetry,
  onDisconnect,
}: {
  root: string;
  status: Load<GitHubStatus>;
  repos: ReposLoad;
  reading: boolean;
  activeJobs: number;
  headingRef?: React.Ref<HTMLHeadingElement>;
  onRetry: () => void;
  onDisconnect: () => Promise<void>;
}) {
  const [asking, setAsking] = useState<"reconnect" | "disconnect" | null>(null);
  const app = status.state === "ready" && status.value.connected ? status.value : undefined;
  return (
    <section aria-labelledby="hub-settings-github" aria-busy={status.state === "loading"} className="space-y-3">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h3 id="hub-settings-github" ref={headingRef} tabIndex={-1} className="flex items-center gap-2 text-base font-semibold outline-none">
            GitHub
            {status.state === "ready" && <ConnectionPill connected={!!app} />}
          </h3>
          <p className="text-sm text-muted-foreground">The hub&apos;s own GitHub App lists your issues, and its jobs push branches and open pull requests as it.</p>
        </div>
        {status.state !== "loading" && (
          <Button
            type="button"
            variant="ghost"
            size="icon"
            className="size-9 shrink-0 rounded-full text-muted-foreground hover:text-foreground"
            aria-label="Read the GitHub setup again"
            title="Read again"
            disabled={reading}
            onClick={onRetry}
          >
            <RefreshCw className={cn(reading && "animate-spin")} />
          </Button>
        )}
      </div>

      {status.state === "loading" && <AppSkeleton />}

      {status.state === "failed" && <ReadError what="Couldn't read the hub's GitHub setup" error={status.error} disabled={reading} onRetry={onRetry} />}

      {status.state === "ready" && !app && <ConnectGitHub root={root} compact />}

      {app && (
        <>
          <AppCard status={app} repos={repos} />
          <ul aria-label="Change the app" className="divide-y overflow-hidden rounded-xl border bg-card">
            <ActionRow
              icon={<RotateCcw />}
              title="Connect a new app"
              body="For another account or organization, or a fresh key. This app keeps working until GitHub hands the new one back."
            >
              <Button type="button" variant="outline" size="sm" className="h-9" onClick={() => setAsking("reconnect")}>
                Reconnect…
              </Button>
            </ActionRow>
            <ActionRow icon={<Unplug />} title="Disconnect" body="The hub forgets the app. It stays on GitHub until you delete it there." danger>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-9 text-destructive hover:bg-destructive/10 hover:text-destructive dark:hover:bg-destructive/15"
                onClick={() => setAsking("disconnect")}
              >
                Disconnect…
              </Button>
            </ActionRow>
          </ul>
          <ReconnectDialog root={root} status={app} open={asking === "reconnect"} onClose={() => setAsking(null)} />
          <DisconnectDialog status={app} activeJobs={activeJobs} open={asking === "disconnect"} onClose={() => setAsking(null)} onDisconnect={onDisconnect} />
        </>
      )}
    </section>
  );
}
