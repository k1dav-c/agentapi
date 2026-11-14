"use client";

import {useEffect, useRef, useState} from "react";
import {ChevronRight, CircleDollarSign, LoaderCircle, RefreshCw, Search} from "lucide-react";
import {Button} from "@/components/ui/button";
import type {createHubAPI} from "@/lib/hub-api";
import {hubErrorText} from "@/lib/hub-api";
import {
  modelSearchText,
  openRouterName,
  priceDetails,
  pricedCount,
  priceSourceText,
  priceText,
  type OpenRouterModel,
  type PriceStatus,
  type Provider,
  type ProviderModelPrice,
} from "@/lib/hub-providers";
import {formatMessageTime} from "@/lib/format-time";
import {cn} from "@/lib/utils";
import {fieldClass} from "./styles";

type HubAPI = ReturnType<typeof createHubAPI>;

// choose saves a model's price choice: an OpenRouter model ID, "none", or
// undefined for automatic; it resolves whether the hub kept it.
type Choose = (model: string, choice: string | undefined) => Promise<boolean>;

// ProviderPrices is a provider's models with the prices the hub found for
// them on OpenRouter, folded under one line in the provider's row: each
// model's price and where it comes from, and a picker for another
// OpenRouter model, no price, or the automatic match. A pick saves the
// list as the editor does.
export function ProviderPrices({api, provider, saving, onChoose}: {api: HubAPI; provider: Provider; saving: boolean; onChoose: Choose}) {
  const [open, setOpen] = useState(false);
  const [picking, setPicking] = useState<string | null>(null);
  const models = provider.model_prices ?? [];
  const priced = pricedCount(provider);
  const summary =
    models.length === 0
      ? provider.models?.length
        ? "Prices"
        : "Prices · refresh them to list its models"
      : `Prices · ${priced} of ${models.length} ${models.length === 1 ? "model" : "models"}`;
  return (
    <div data-provider-prices>
      <button
        type="button"
        aria-expanded={open}
        onClick={() => setOpen((shown) => !shown)}
        className="inline-flex items-center gap-1 rounded-md text-xs text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/40"
      >
        <ChevronRight className={cn("size-3 transition-transform", open && "rotate-90")} aria-hidden="true" />
        <CircleDollarSign className="size-3" aria-hidden="true" />
        {summary}
      </button>
      {open && models.length > 0 && (
        <ul aria-label={`Prices of ${provider.name || provider.id}`} className="mt-1.5 space-y-1.5">
          {models.map((model) => (
            <li key={model.model} data-model-price={model.model} className="rounded-lg border bg-background px-2.5 py-1.5">
              <div className="flex min-w-0 items-center gap-2">
                <span className="min-w-0 flex-1">
                  <span className="block truncate font-mono text-[11px]">{model.name || model.model}</span>
                  {/* The source shortens first; the price and its badge stay whole. */}
                  <span className="flex min-w-0 items-center text-xs" title={model.price ? priceDetails(model.price) : undefined}>
                    {model.price ? <span className="shrink-0 font-medium tabular-nums">{priceText(model.price)}</span> : <span className="shrink-0 text-muted-foreground">No price</span>}
                    <span className="min-w-0 truncate whitespace-pre text-muted-foreground"> · {priceSourceText(model)}</span>
                    {model.choice === "auto" && model.price && <ChoiceBadge>auto</ChoiceBadge>}
                    {model.choice === "chosen" && <ChoiceBadge>picked</ChoiceBadge>}
                  </span>
                </span>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-7 shrink-0 px-2 text-xs"
                  aria-label={`Change the price of ${model.model}`}
                  aria-expanded={picking === model.model}
                  onClick={() => setPicking((current) => (current === model.model ? null : model.model))}
                >
                  Change
                </Button>
              </div>
              {picking === model.model && (
                <PricePicker
                  api={api}
                  model={model}
                  saving={saving}
                  onChoose={async (choice) => {
                    if (await onChoose(model.model, choice)) setPicking(null);
                  }}
                  onCancel={() => setPicking(null)}
                />
              )}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function ChoiceBadge({children}: {children: React.ReactNode}) {
  return <span className="ml-1.5 shrink-0 rounded-full border px-1.5 py-px text-[10px] text-muted-foreground">{children}</span>;
}

// PricePicker searches the hub's copy of OpenRouter's list for the model
// to price model at, starting from the name the hub matches it by; it also
// offers no price, and the automatic match back.
function PricePicker({
  api,
  model,
  saving,
  onChoose,
  onCancel,
}: {
  api: HubAPI;
  model: ProviderModelPrice;
  saving: boolean;
  onChoose: (choice: string | undefined) => void;
  onCancel: () => void;
}) {
  const [query, setQuery] = useState(() => modelSearchText(model.model));
  const [found, setFound] = useState<OpenRouterModel[] | null>(null);
  const [error, setError] = useState("");
  // An answer counts only for the latest query.
  const generation = useRef(0);
  useEffect(() => {
    const current = ++generation.current;
    api.searchOpenRouter(query).then(
      (models) => {
        if (generation.current !== current) return;
        setFound(models);
        setError("");
      },
      (failure: unknown) => generation.current === current && setError(hubErrorText(failure)),
    );
  }, [api, query]);
  useEffect(
    () => () => {
      generation.current++;
    },
    [],
  );
  return (
    <div role="group" aria-label={`Price of ${model.model}`} className="mt-2 space-y-2 border-t pt-2">
      <label className="relative block">
        <Search className="pointer-events-none absolute inset-y-0 left-2.5 my-auto size-3.5 text-muted-foreground" aria-hidden="true" />
        <input
          autoFocus
          value={query}
          onChange={(event) => setQuery(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.stopPropagation();
              onCancel();
            }
          }}
          placeholder="Search OpenRouter models"
          aria-label="Search OpenRouter models"
          spellCheck={false}
          className={cn(fieldClass, "h-8 pl-8 font-mono text-xs")}
        />
      </label>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
      <div role="listbox" aria-label="OpenRouter models" className="max-h-48 space-y-0.5 overflow-y-auto">
        {found?.length === 0 && <p className="px-1 py-1 text-xs text-muted-foreground">No OpenRouter model has these words. Refresh the prices if the list is old.</p>}
        {found?.map((each) => {
          const current = model.choice !== "none" && model.openrouter === each.id;
          return (
            <button
              key={each.id}
              type="button"
              role="option"
              aria-selected={current}
              disabled={saving}
              onClick={() => onChoose(each.id)}
              className={cn(
                "flex w-full min-w-0 items-baseline gap-2 rounded-md px-2 py-1 text-left text-xs outline-none hover:bg-muted focus-visible:ring-[3px] focus-visible:ring-ring/40 disabled:opacity-50",
                current && "bg-muted",
              )}
            >
              <span className="min-w-0 flex-1 truncate">
                <span className="font-medium">{openRouterName({openrouter: each.id, openrouter_name: each.name})}</span>{" "}
                <span className="font-mono text-[11px] text-muted-foreground">{each.id}</span>
              </span>
              <span className="shrink-0 tabular-nums text-muted-foreground" title={priceDetails(each.price)}>
                {priceText(each.price)}
              </span>
            </button>
          );
        })}
      </div>
      <div className="flex flex-wrap items-center justify-end gap-1.5">
        {saving && <LoaderCircle className="size-3.5 animate-spin text-muted-foreground" aria-label="Saving" />}
        {model.choice !== "auto" && (
          <Button type="button" variant="outline" size="sm" className="h-7 text-xs" disabled={saving} onClick={() => onChoose(undefined)}>
            Automatic
          </Button>
        )}
        {model.choice !== "none" && (
          <Button type="button" variant="outline" size="sm" className="h-7 text-xs" disabled={saving} onClick={() => onChoose("none")}>
            No price
          </Button>
        )}
        <Button type="button" variant="ghost" size="sm" className="h-7 text-xs" onClick={onCancel}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

// RefreshPrices has the hub get OpenRouter's prices again, and the
// providers that list their models list them.
export function RefreshPrices({refreshing, onRefresh}: {refreshing: boolean; onRefresh: () => void}) {
  return (
    <Button type="button" variant="ghost" size="sm" className="h-9 shrink-0" disabled={refreshing} onClick={onRefresh}>
      {refreshing ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
      Refresh prices
    </Button>
  );
}

// PricesNote says where the prices come from and how old they are: list
// prices, an estimate for a gateway that serves the models itself.
export function PricesNote({status}: {status: PriceStatus}) {
  return (
    <p className="flex gap-2 text-xs leading-5 text-pretty text-muted-foreground" data-prices-note>
      <CircleDollarSign className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
      <span>
        Costs of calls to these models use OpenRouter&apos;s list prices
        {status.fetched_at ? `, as of ${formatMessageTime(status.fetched_at)}` : ", once the hub has them"}: an estimate for a gateway that serves them itself.
        {status.error && <span className="text-destructive"> The last refresh failed: {status.error}</span>}
      </span>
    </p>
  );
}
