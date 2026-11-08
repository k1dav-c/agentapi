"use client";

import {Button} from "@/components/ui/button";
import type {HubInstance} from "@/lib/hub";
import {nothingMatches, stoppedShown} from "@/lib/hub-filter";
import type {ListOrder} from "@/lib/hub-order";
import {HubMark} from "./brand";
import {GroupHeader, InstanceRow} from "./instance-row";
import {pinPlace} from "./list-context";
import {MachineCard} from "./machine-card";
import {Onboarding} from "./overview";
import {useRowFocusRestore} from "./use-row-focus-restore";
import type {ListFilters} from "./use-list-filters";

export interface HubListProps {
  root: string;
  filters: ListFilters;
  order: ListOrder;
  // collapsed are the sections folded (useCollapsedGroups).
  collapsed: ReadonlySet<string>;
  onToggle: (key: string) => void;
  onRename: (group: string) => void;
  // selected is the instance open beside the list, if any.
  selected?: string;
  onAdd: () => void;
  // onForgetStopped asks to forget the stopped ones the Offline filter shows.
  onForgetStopped: (instances: HubInstance[]) => void;
}

// HubList is the hub's list of agents: its sections (the pinned ones, the
// groups or the recent order's rows), each a block with a header that
// folds it, its instances as rows or, by machine, as cards; the guide of an
// empty hub, or what left nothing to show. The rows and cards take what
// they share from HubListContext.
export function HubList({root, filters, order, collapsed, onToggle, onRename, selected, onAdd, onForgetStopped}: HubListProps) {
  const {sections, counts, filtered} = filters;
  const focus = useRowFocusRestore(sections);
  const stopped = stoppedShown(filters.filter, sections.flatMap((section) => section.instances));
  return (
    <ul className="flex-1 overflow-y-auto overscroll-contain pb-[max(0.75rem,env(safe-area-inset-bottom))]" aria-label="Instances" data-hub-list {...focus}>
      {stopped.length > 0 && (
        <li>
          <ForgetStoppedRow count={stopped.length} onForget={() => onForgetStopped(stopped)} />
        </li>
      )}
      {counts.all === 0 && (
        <li>
          <div className="md:hidden">
            <Onboarding root={root} onAdd={onAdd} compact />
          </div>
          <div className="hidden px-6 py-12 text-center md:block">
            <HubMark className="mx-auto size-10 opacity-60" />
            <p className="mt-4 text-sm font-medium">No agents yet</p>
            <p className="mt-1 text-sm text-muted-foreground">The steps on the right connect them.</p>
          </div>
        </li>
      )}
      {counts.all > 0 && sections.length === 0 && (
        <li className="px-6 py-12 text-center">
          <p className="text-sm font-medium">Nothing matches</p>
          <p className="mt-1 text-sm text-muted-foreground">{nothingMatches(filters.query, filters.filter, filters.agents)}.</p>
          <Button variant="outline" size="sm" className="mt-4" onClick={filters.showAll}>
            Show all
          </Button>
        </li>
      )}
      {sections.map((section) => {
        // A search or a filter shows every match, folded or not.
        const folded = !!section.label && !filtered && collapsed.has(section.key);
        // Each section is a block of its own, so its header sticks only
        // while its rows scroll by, and the next header pushes it off.
        return (
          <li key={`section:${section.key}`}>
            {section.label && <GroupHeader group={section} label={section.label} folded={folded} onToggle={() => onToggle(section.key)} onRename={() => onRename(section.name)} />}
            {!folded && section.machines && (
              <ul aria-label={section.label || undefined}>
                {section.machines.map((machine) =>
                  machine.hostname ? (
                    <MachineCard key={machine.key} card={`${section.key}\n${machine.key}`} machine={machine} order={order} selected={selected} />
                  ) : (
                    // An instance that doesn't name its machine is a row of its own.
                    <InstanceRow key={machine.key} instance={machine.instances[0]} order={order} selected={machine.instances[0].id === selected} />
                  ),
                )}
              </ul>
            )}
            {!folded && !section.machines && (
              <ul aria-label={section.label || undefined}>
                {section.instances.map((instance, index) => (
                  <InstanceRow
                    key={instance.id}
                    instance={instance}
                    order={order}
                    selected={instance.id === selected}
                    pinned={section.pinned ? pinPlace(index === 0, index === section.instances.length - 1) : undefined}
                  />
                ))}
              </ul>
            )}
          </li>
        );
      })}
    </ul>
  );
}

// ForgetStoppedRow is the slim row atop the Offline filter's list that
// forgets the stopped ones it shows.
export function ForgetStoppedRow({count, onForget}: {count: number; onForget: () => void}) {
  return (
    <div className="flex items-center gap-1.5 border-b bg-state-fault/5 px-4 py-2 text-xs text-muted-foreground">
      <span className="tabular-nums">{count} stopped</span>
      <span aria-hidden="true" className="text-muted-foreground/40">
        ·
      </span>
      <button
        type="button"
        onClick={onForget}
        className="rounded-md px-1.5 py-0.5 font-medium text-foreground underline-offset-4 outline-none hover:text-destructive hover:underline focus-visible:ring-[3px] focus-visible:ring-ring/50"
      >
        Forget all
      </button>
    </div>
  );
}
