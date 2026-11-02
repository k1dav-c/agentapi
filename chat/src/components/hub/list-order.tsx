"use client";

import {useCallback, useState} from "react";
import {History, ListTree, type LucideIcon} from "lucide-react";
import {parseListOrder, type ListOrder} from "@/lib/hub-order";
import {cn} from "@/lib/utils";

// Where the browser keeps the list's order.
const orderKey = "agentapi.hub.list-order";

function storedOrder(): ListOrder {
  try {
    return parseListOrder(localStorage.getItem(orderKey));
  } catch {
    return "groups";
  }
}

// useListOrder is the order of the hub's list this browser keeps: its
// groups, or the latest change first.
export function useListOrder(): [ListOrder, (order: ListOrder) => void] {
  const [order, setOrder] = useState(storedOrder);
  const change = useCallback((next: ListOrder) => {
    setOrder(next);
    try {
      localStorage.setItem(orderKey, next);
    } catch {}
  }, []);
  return [order, change];
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
