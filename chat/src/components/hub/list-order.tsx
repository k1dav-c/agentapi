"use client";

import {useCallback, useState} from "react";
import {History, ListTree, Server, type LucideIcon} from "lucide-react";
import {storedView, type ListOrder} from "@/lib/hub-order";
import {cn} from "@/lib/utils";

// Where the browser keeps the list's order, and whether by machine.
const orderKey = "agentapi.hub.list-order";
const byMachineKey = "agentapi.hub.by-machine";

// storedListView reads them; an order of "machines" from an earlier build
// is kept again as the groups by machine.
function storedListView(): {order: ListOrder; byMachine: boolean} {
  try {
    const order = localStorage.getItem(orderKey);
    const view = storedView(order, localStorage.getItem(byMachineKey));
    if (order === "machines") {
      localStorage.setItem(orderKey, view.order);
      localStorage.setItem(byMachineKey, String(view.byMachine));
    }
    return view;
  } catch {
    return {order: "groups", byMachine: false};
  }
}

function keep(key: string, value: string) {
  try {
    localStorage.setItem(key, value);
  } catch {}
}

// useListView is the order of the hub's list this browser keeps (its
// groups, or the latest change first) and whether it puts each machine's
// agents together.
export function useListView(): {order: ListOrder; setOrder: (order: ListOrder) => void; byMachine: boolean; setByMachine: (on: boolean) => void} {
  const [view, setView] = useState(storedListView);
  const setOrder = useCallback((order: ListOrder) => {
    setView((previous) => ({...previous, order}));
    keep(orderKey, order);
  }, []);
  const setByMachine = useCallback((byMachine: boolean) => {
    setView((previous) => ({...previous, byMachine}));
    keep(byMachineKey, String(byMachine));
  }, []);
  return {...view, setOrder, setByMachine};
}

const orders: {id: ListOrder; label: string; title: string; Icon: LucideIcon}[] = [
  {id: "groups", label: "Groups", title: "Groups: what needs you first in each", Icon: ListTree},
  {id: "recent", label: "Recent changes", title: "Recent changes: the latest first, without groups", Icon: History},
];

// ListOrderSwitch picks the list's order, beside the search.
export function ListOrderSwitch({order, onChange}: {order: ListOrder; onChange: (order: ListOrder) => void}) {
  return (
    <div role="group" aria-label="Order the agents" className="flex h-10 shrink-0 items-center gap-0.5 rounded-xl border bg-muted/50 p-[3px]">
      {orders.map(({id, label, title, Icon}) => {
        const active = order === id;
        return (
          <button
            key={id}
            type="button"
            aria-pressed={active}
            aria-label={label}
            title={title}
            onClick={() => onChange(id)}
            className={cn(
              "flex h-full w-9 items-center justify-center rounded-[9px] outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
              active ? "bg-background text-foreground shadow-xs ring-1 ring-border dark:bg-accent" : "text-muted-foreground hover:text-foreground",
            )}
          >
            <Icon className="size-4" aria-hidden="true" />
          </button>
        );
      })}
    </div>
  );
}

// ByMachineToggle puts each machine's agents together in a card, in either
// order; beside the order's switch.
export function ByMachineToggle({on, onChange}: {on: boolean; onChange: (on: boolean) => void}) {
  return (
    <button
      type="button"
      aria-pressed={on}
      aria-label="By machine"
      title={on ? "By machine: each machine's agents together, a tab each" : "By machine: put each machine's agents together, a tab each"}
      onClick={() => onChange(!on)}
      className={cn(
        "flex size-10 shrink-0 items-center justify-center rounded-xl border outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/50",
        on ? "bg-background text-foreground shadow-xs ring-1 ring-border dark:bg-accent" : "bg-muted/50 text-muted-foreground hover:text-foreground",
      )}
    >
      <Server className="size-4" aria-hidden="true" />
    </button>
  );
}
