"use client";

import {useRef, useState} from "react";
import * as TabsPrimitive from "@radix-ui/react-tabs";
import {Cloud, Github, Star} from "lucide-react";
import {Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle} from "@/components/ui/dialog";
import type {HubInstance} from "@/lib/hub";
import type {Favorite} from "@/lib/hub-favorites";
import {FavoritesSettings} from "./favorites-settings";
import {GitHubSettings} from "./github-settings";
import {ProvidersSettings} from "./providers-settings";

// The sections of the hub's settings, each a tab of the dialog; opening it
// at one shows that one.
export type SettingsSection = "favorites" | "providers" | "github";

const sections: {id: SettingsSection; label: string; icon: React.ReactNode}[] = [
  {id: "favorites", label: "Favorites", icon: <Star />},
  {id: "providers", label: "Providers", icon: <Cloud />},
  {id: "github", label: "GitHub", icon: <Github />},
];

// An editor of a section: whether it is open, and what closes it.
type Editor = {open: boolean; cancel: () => void};

// HubSettings is the dialog of the settings kept on the hub, which every
// browser signed in to it shares: the favorites, the model providers the
// AgentAPIs that report to it get, and the hub's GitHub App. section is the
// one it opens at; null is closed. activeJobs is how many jobs at work push
// with the app's tokens; instances, the agents whose use of each provider
// the Providers section shows.
export function HubSettings({
  root,
  section,
  onClose,
  favorites,
  onFavoritesSaved,
  activeJobs,
  instances,
}: {
  root: string;
  section: SettingsSection | null;
  onClose: () => void;
  favorites: Favorite[] | undefined;
  onFavoritesSaved: (favorites: Favorite[]) => void;
  activeJobs?: number;
  instances?: HubInstance[];
}) {
  // The favorites' and the providers' editors: Escape closes the one of the
  // section shown.
  const editors = {
    favorites: useRef<Editor | null>(null),
    providers: useRef<Editor | null>(null),
  };
  const headings = useRef<Partial<Record<SettingsSection, HTMLHeadingElement | null>>>({});
  const [shown, setShown] = useState<SettingsSection>(section ?? "favorites");
  // The Providers and GitHub sections read the hub: only once they show,
  // again each time the dialog opens.
  const [seen, setSeen] = useState<ReadonlySet<SettingsSection>>(new Set(section ? [section] : []));
  const show = (next: SettingsSection) => {
    setShown(next);
    setSeen((all) => (all.has(next) ? all : new Set([...all, next])));
  };
  // Opened, or at another section: that one, before the dialog draws.
  const [asked, setAsked] = useState(section);
  if (section !== asked) {
    setAsked(section);
    if (section) show(section);
    else setSeen(new Set());
  }
  return (
    <Dialog open={section !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent
        className="max-h-[90dvh] grid-cols-[minmax(0,1fr)] overflow-y-auto sm:max-w-xl"
        onOpenAutoFocus={(event) => {
          const heading = section ? headings.current[section] : null;
          if (!heading) return;
          event.preventDefault();
          heading.focus({preventScroll: true});
        }}
        // Escape closes the editor first, then the dialog; a click outside
        // leaves an open editor be.
        onEscapeKeyDown={(event) => {
          const editor = shown === "github" ? null : editors[shown].current;
          if (!editor?.open) return;
          event.preventDefault();
          editor.cancel();
        }}
        onInteractOutside={(event) => {
          if (editors.favorites.current?.open || editors.providers.current?.open) event.preventDefault();
        }}
      >
        <DialogHeader>
          <DialogTitle>Hub settings</DialogTitle>
          <DialogDescription>Kept on the hub: every browser signed in to it has the same.</DialogDescription>
        </DialogHeader>
        <TabsPrimitive.Root value={shown} onValueChange={(value) => show(value as SettingsSection)} className="min-w-0 space-y-5">
          <TabsPrimitive.List aria-label="Settings" className="relative flex gap-1 rounded-xl bg-muted p-1">
            {sections.map(({id, label, icon}) => (
              <TabsPrimitive.Trigger
                key={id}
                value={id}
                className="inline-flex h-8 flex-1 items-center justify-center gap-1.5 rounded-lg text-sm font-medium text-muted-foreground outline-none transition-[color,background-color,box-shadow] duration-200 hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/40 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm dark:data-[state=active]:bg-input/40 [&_svg]:size-3.5"
              >
                {icon}
                {label}
              </TabsPrimitive.Trigger>
            ))}
          </TabsPrimitive.List>
          {/* They stay mounted: a half-written favorite outlives a look at GitHub. */}
          <TabsPrimitive.Content value="favorites" forceMount className="outline-none data-[state=inactive]:hidden">
            <FavoritesSettings
              root={root}
              favorites={favorites}
              onSaved={onFavoritesSaved}
              editor={editors.favorites}
              headingRef={(heading) => {
                headings.current.favorites = heading;
              }}
            />
          </TabsPrimitive.Content>
          <TabsPrimitive.Content value="providers" forceMount className="outline-none data-[state=inactive]:hidden">
            {seen.has("providers") && (
              <ProvidersSettings
                root={root}
                instances={instances}
                editor={editors.providers}
                headingRef={(heading) => {
                  headings.current.providers = heading;
                }}
              />
            )}
          </TabsPrimitive.Content>
          <TabsPrimitive.Content value="github" forceMount className="outline-none data-[state=inactive]:hidden">
            {seen.has("github") && (
              <GitHubSettings
                root={root}
                activeJobs={activeJobs}
                headingRef={(heading) => {
                  headings.current.github = heading;
                }}
              />
            )}
          </TabsPrimitive.Content>
        </TabsPrimitive.Root>
      </DialogContent>
    </Dialog>
  );
}
