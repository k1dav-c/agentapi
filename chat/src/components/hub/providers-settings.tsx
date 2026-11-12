"use client";

import {useEffect, useMemo, useRef, useState} from "react";
import {
  Activity,
  Bot,
  Check,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  CircleX,
  Cloud,
  Eye,
  EyeOff,
  LoaderCircle,
  LockKeyhole,
  Pencil,
  Plug,
  Plus,
  Server,
  ShieldCheck,
  Sparkles,
  Trash2,
} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {Kbd} from "@/components/ui/kbd";
import type {HubInstance} from "@/lib/hub";
import {createHubAPI, HubAPIError, hubErrorText} from "@/lib/hub-api";
import {
  changeOf,
  checkFailureText,
  draftOf,
  draftProblem,
  keptChange,
  maxProviders,
  parseModelIDs,
  providerEndpoint,
  providerHue,
  providerModelsText,
  providerMonogram,
  providerName,
  toggleModel,
  type Provider,
  type ProviderChange,
  type ProviderDraft,
  type ProviderModel,
  type ProviderSnapshot,
} from "@/lib/hub-providers";
import {providerPeriod, providerTitle, providerTotals, type ProviderTotal} from "@/lib/hub-spend";
import {cn} from "@/lib/utils";
import {fieldClass} from "./styles";

// What the editor edits: a provider by its ID, or a new one (null).
type Editing = {id: string | null};

// ProvidersSettings is the Providers section of the hub's settings: the
// model providers kept on the hub, which the AgentAPIs that report to it
// get, keys included. It reads them when it first shows. Each change saves
// the whole list (PUT /api/providers); the list shows the hub's answer, and
// a refused change keeps the editor open with a toast. editor tells the
// dialog whether the editor is open, and closes it: Escape closes the
// editor first, and a click outside doesn't lose what it holds. Each
// provider says what the instances' agents used it for, once some report it.
export function ProvidersSettings({
  root,
  instances,
  editor,
  headingRef,
}: {
  root: string;
  instances?: HubInstance[];
  editor: React.RefObject<{open: boolean; cancel: () => void} | null>;
  headingRef?: React.Ref<HTMLHeadingElement>;
}) {
  const api = useMemo(() => createHubAPI(root), [root]);
  const usage = useMemo(() => providerTotals(instances ?? []), [instances]);
  const [snapshot, setSnapshot] = useState<ProviderSnapshot | undefined>(undefined);
  const [loadError, setLoadError] = useState("");
  const [editing, setEditing] = useState<Editing | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);
  // A save may close only the operation that started it, not an editor or
  // confirmation opened while its request was under way.
  const operation = useRef(0);

  useEffect(() => {
    let current = true;
    api.getProviders().then(
      (answer) => current && setSnapshot(answer),
      (error: unknown) => current && setLoadError(hubErrorText(error)),
    );
    return () => {
      current = false;
    };
  }, [api]);

  // Closed with the dialog, it leaves no editor open behind.
  useEffect(
    () => () => {
      editor.current = null;
    },
    [editor],
  );
  useEffect(() => {
    editor.current = {
      open: editing !== null || deleting !== null,
      cancel: () => {
        operation.current++;
        setEditing(null);
        setDeleting(null);
      },
    };
  });

  // save sends the whole list at the revision last read. A conflict gets the
  // current list without touching the editor, whose draft may still be useful.
  const save = async (next: ProviderChange[]): Promise<boolean> => {
    if (!snapshot) return false;
    setSaving(true);
    try {
      setSnapshot(await api.setProviders(snapshot.revision, next));
      return true;
    } catch (error) {
      if (error instanceof HubAPIError && error.status === 409) {
        toast.error("The providers changed elsewhere", {description: "Your draft is still open. Review the latest list before saving again."});
        try {
          setSnapshot(await api.getProviders());
        } catch (refreshError) {
          toast.error("Couldn't read the latest providers", {description: hubErrorText(refreshError)});
        }
      } else {
        toast.error("Couldn't save the providers", {description: hubErrorText(error)});
      }
      return false;
    } finally {
      setSaving(false);
    }
  };

  const list = snapshot?.providers ?? [];
  // commit saves draft in place of previous, or after the others.
  const commit = async (draft: ProviderDraft, previous: Provider | undefined) => {
    const started = operation.current;
    const change = changeOf(draft, previous);
    const next = previous ? list.map((each) => (each.id === previous.id ? change : keptChange(each))) : [...list.map(keptChange), change];
    if ((await save(next)) && operation.current === started) setEditing(null);
  };
  const remove = async (provider: Provider) => {
    const started = operation.current;
    if ((await save(list.filter((each) => each.id !== provider.id).map(keptChange))) && operation.current === started) setDeleting(null);
  };

  const full = list.length >= maxProviders;
  const adding = editing?.id === null;
  const startAdding = () => {
    operation.current++;
    setDeleting(null);
    setEditing({id: null});
  };
  const cancelEditing = () => {
    operation.current++;
    setEditing(null);
  };
  return (
    <section aria-labelledby="hub-settings-providers" className="space-y-4">
      <div className="flex items-start gap-3">
        <div className="min-w-0 flex-1">
          <h3 id="hub-settings-providers" ref={headingRef} tabIndex={-1} className="flex items-baseline gap-2 text-base font-semibold outline-none">
            Providers
            {list.length > 0 && (
              <span className="text-xs font-normal text-muted-foreground tabular-nums">
                {list.length}/{maxProviders}
              </span>
            )}
          </h3>
          <p className="text-sm text-pretty text-muted-foreground">
            Gateways your Claude Code and Codex sessions can switch to. Every machine joined to this hub gets them within a minute.
          </p>
        </div>
        {list.length > 0 && (
          <Button
            type="button"
            variant="outline"
            size="sm"
            className="h-9 shrink-0"
            disabled={full || adding || saving}
            title={full ? `The hub keeps at most ${maxProviders}` : undefined}
            onClick={startAdding}
          >
            <Plus /> Add
          </Button>
        )}
      </div>

      {snapshot === undefined &&
        (loadError ? (
          <p role="alert" className="text-sm text-destructive">
            Couldn&apos;t read the providers: {loadError}
          </p>
        ) : (
          <div aria-busy="true" aria-label="Loading the providers" className="space-y-2">
            {[0, 1].map((each) => (
              <div key={each} className="h-[76px] animate-pulse rounded-2xl border bg-muted/40" />
            ))}
          </div>
        ))}

      {snapshot !== undefined && list.length === 0 && !adding && (
        <div className="relative overflow-hidden rounded-2xl border border-dashed px-6 py-9 text-center">
          <div aria-hidden="true" className="mx-auto mb-4 flex w-fit items-center gap-2 text-muted-foreground">
            <span className="grid size-9 place-items-center rounded-xl border bg-card shadow-xs">
              <Bot className="size-4" />
            </span>
            <span className="h-px w-8 bg-gradient-to-r from-border to-foreground/30" />
            <span className="grid size-11 place-items-center rounded-2xl bg-foreground text-background shadow-sm">
              <Cloud className="size-5" />
            </span>
            <span className="h-px w-8 bg-gradient-to-l from-border to-foreground/30" />
            <span className="grid size-9 place-items-center rounded-xl border bg-card shadow-xs">
              <Server className="size-4" />
            </span>
          </div>
          <p className="text-sm font-medium">No providers yet</p>
          <p className="mx-auto mt-1 max-w-sm text-sm text-pretty text-muted-foreground">
            Add a LiteLLM, vLLM or other OpenAI- or Anthropic-compatible gateway once, and every session on every machine can switch to its models.
          </p>
          <Button type="button" size="sm" className="mt-5 h-9" onClick={startAdding}>
            <Plus /> Add a provider
          </Button>
        </div>
      )}

      {list.length > 0 && (
        <ul aria-label="Providers" className="space-y-2">
          {list.map((provider) =>
            editing?.id === provider.id ? (
              <li key={provider.id} className="rounded-2xl border bg-card p-4 shadow-xs ring-1 ring-ring/10">
                <ProviderEditor
                  api={api}
                  provider={provider}
                  others={list.filter((each) => each.id !== provider.id).map((each) => each.id)}
                  saving={saving}
                  onSave={(draft) => void commit(draft, provider)}
                  onCancel={cancelEditing}
                />
              </li>
            ) : deleting === provider.id ? (
              <li
                key={provider.id}
                role="group"
                aria-label={`Delete ${providerName(provider)}`}
                className="flex items-center gap-3 rounded-2xl border border-destructive/30 bg-destructive/5 py-3 pr-3 pl-4"
              >
                <Trash2 className="size-4 shrink-0 text-destructive" aria-hidden="true" />
                <p className="min-w-0 flex-1 text-sm">
                  Delete <span className="font-medium">{providerName(provider)}</span>
                  {provider.has_key && " and its key"}? Sessions on it go back to their own provider when they start again.
                </p>
                <Button
                  type="button"
                  variant="ghost"
                  size="sm"
                  className="h-8 shrink-0"
                  onClick={() => {
                    operation.current++;
                    setDeleting(null);
                  }}
                >
                  Cancel
                </Button>
                <Button autoFocus type="button" variant="destructive" size="sm" className="h-8 shrink-0" disabled={saving} onClick={() => void remove(provider)}>
                  {saving && <LoaderCircle className="animate-spin" />}
                  Delete
                </Button>
              </li>
            ) : (
              <ProviderRow
                key={provider.id}
                provider={provider}
                usage={usage.counted > 0 ? (usage.providers.find((total) => total.provider === provider.id) ?? null) : undefined}
                onEdit={() => {
                  operation.current++;
                  setDeleting(null);
                  setEditing({id: provider.id});
                }}
                onDelete={() => {
                  operation.current++;
                  setDeleting(provider.id);
                }}
              />
            ),
          )}
        </ul>
      )}

      {adding && (
        <div className="rounded-2xl border bg-card p-4 shadow-xs ring-1 ring-ring/10">
          <ProviderEditor api={api} others={list.map((each) => each.id)} saving={saving} onSave={(draft) => void commit(draft, undefined)} onCancel={cancelEditing} />
        </div>
      )}

      {list.length > 0 && (
        <p className="flex gap-2 text-xs leading-5 text-pretty text-muted-foreground">
          <ShieldCheck className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
          <span>
            Keys travel sealed with the hub&apos;s secret, and every joined machine gets them; an agent running on a provider can read its key. A provider with
            the same ID in a machine&apos;s <code className="font-mono">~/.agentapi/config.yaml</code> wins there.
          </span>
        </p>
      )}
    </section>
  );
}

// ProviderTile is a provider's monogram, in a hue of its own.
function ProviderTile({provider, className}: {provider: {id: string; name?: string}; className?: string}) {
  const hue = providerHue(provider.id);
  return (
    <span
      aria-hidden="true"
      style={{"--hue": hue} as React.CSSProperties}
      className={cn(
        "grid size-10 shrink-0 place-items-center rounded-xl text-[13px] font-semibold tracking-tight",
        "bg-[oklch(0.94_0.04_var(--hue))] text-[oklch(0.42_0.12_var(--hue))] ring-1 ring-[oklch(0.42_0.12_var(--hue)/0.12)] ring-inset",
        "dark:bg-[oklch(0.32_0.06_var(--hue))] dark:text-[oklch(0.88_0.08_var(--hue))] dark:ring-[oklch(0.88_0.08_var(--hue)/0.12)]",
        className,
      )}
    >
      {providerMonogram(providerName(provider))}
    </span>
  );
}

// ProviderRow is a provider in the list: who it is, where it runs, its
// models, whether it has a key, and what the agents used it for (usage:
// null for nothing in the last 7 days, none while no agent reports it).
// Its buttons show on hover and focus where there is a pointer, always on a
// touch screen.
function ProviderRow({
  provider,
  usage,
  onEdit,
  onDelete,
}: {
  provider: Provider;
  usage?: ProviderTotal | null;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const models = provider.models ?? [];
  const shown = models.slice(0, 3);
  return (
    <li
      data-provider={provider.id}
      className="group relative flex items-center gap-3 rounded-2xl border bg-card py-3 pr-2 pl-3 transition-[border-color,box-shadow] hover:border-foreground/15 hover:shadow-xs"
    >
      <ProviderTile provider={provider} />
      <div className="min-w-0 flex-1 space-y-1">
        <p className="flex min-w-0 items-center gap-2">
          <span className="truncate text-sm font-medium">{providerName(provider)}</span>
          <KeyBadge hasKey={provider.has_key} />
        </p>
        <p className="flex min-w-0 items-center gap-1.5 font-mono text-xs text-muted-foreground">
          {provider.name && (
            <>
              <span className="shrink-0">{provider.id}</span>
              <span aria-hidden="true" className="opacity-50">
                ·
              </span>
            </>
          )}
          <span className="truncate" title={provider.base_url}>
            {providerEndpoint(provider.base_url)}
          </span>
        </p>
        <div className="flex min-w-0 flex-wrap items-center gap-1" aria-label={providerModelsText(provider)}>
          {shown.length === 0 ? (
            <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
              <Sparkles className="size-3" aria-hidden="true" />
              Its models, as it lists them
            </span>
          ) : (
            <>
              {shown.map((model) => (
                <span key={model.id} className="max-w-40 truncate rounded-full border bg-background px-2 py-px font-mono text-[11px]">
                  {model.name || model.id}
                </span>
              ))}
              {models.length > shown.length && <span className="text-[11px] text-muted-foreground tabular-nums">+{models.length - shown.length}</span>}
            </>
          )}
        </div>
        {usage !== undefined && <ProviderUsage usage={usage} />}
      </div>
      <div className="flex shrink-0 items-center transition-opacity [@media(hover:hover)]:opacity-0 [@media(hover:hover)]:group-focus-within:opacity-100 [@media(hover:hover)]:group-hover:opacity-100">
        <IconButton label={`Edit ${providerName(provider)}`} onClick={onEdit}>
          <Pencil />
        </IconButton>
        <IconButton label={`Delete ${providerName(provider)}`} className="hover:text-destructive" onClick={onDelete}>
          <Trash2 />
        </IconButton>
      </div>
    </li>
  );
}

// ProviderUsage is a provider's tokens and their cost in the last 24 hours
// and 7 days, of the agents that report them, each scope once.
function ProviderUsage({usage}: {usage: ProviderTotal | null}) {
  return (
    <p className="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground tabular-nums" title={usage ? providerTitle(usage) : undefined} data-provider-usage>
      <Activity className="size-3 shrink-0" aria-hidden="true" />
      <span className="truncate">
        {usage ? `24h ${providerPeriod(usage.day, usage.dayTokens)} · 7d ${providerPeriod(usage.week, usage.weekTokens)}` : "No calls in the last 7 days"}
      </span>
    </p>
  );
}

// KeyBadge says whether a provider has a key, which the hub doesn't show.
function KeyBadge({hasKey}: {hasKey: boolean}) {
  return hasKey ? (
    <span className="inline-flex shrink-0 items-center gap-1 rounded-full bg-emerald-500/10 px-1.5 py-px text-[11px] font-medium text-emerald-700 dark:text-emerald-400">
      <LockKeyhole className="size-3" aria-hidden="true" />
      Key
    </span>
  ) : (
    <span className="inline-flex shrink-0 items-center rounded-full border border-dashed px-1.5 py-px text-[11px] text-muted-foreground">No key</span>
  );
}

function IconButton({label, className, ...props}: React.ComponentProps<typeof Button> & {label: string}) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="icon"
      aria-label={label}
      title={label}
      className={cn("size-9 rounded-full text-muted-foreground hover:text-foreground disabled:opacity-30", className)}
      {...props}
    />
  );
}

// The answer of a connection check: the models the provider listed and how
// fast, or why it failed; checking while it runs.
type Check =
  | {state: "checking"}
  | {state: "ok"; models: ProviderModel[]; latency: number}
  | {state: "failed"; reason: string};

// ProviderEditor edits a provider, or a new one without provider. Its ID is
// fixed once it is added: the hub keeps a key by ID, and sessions name the
// provider by it. The key is write-only: empty keeps the one it has. Check
// connection asks the provider for its models from the hub, with the key
// typed or kept, and offers them to pick. ⌘/Ctrl+Enter saves.
function ProviderEditor({
  api,
  provider,
  others,
  saving,
  onSave,
  onCancel,
}: {
  api: ReturnType<typeof createHubAPI>;
  provider?: Provider;
  others: readonly string[];
  saving: boolean;
  onSave: (draft: ProviderDraft) => void;
  onCancel: () => void;
}) {
  const [draft, setDraft] = useState(() => draftOf(provider));
  const [check, setCheck] = useState<Check | null>(null);
  const checkGeneration = useRef(0);
  const [showKey, setShowKey] = useState(false);
  const [advanced, setAdvanced] = useState(() => !!provider?.anthropic_base_url);
  // A change of the URL or the key makes the last check stale, including one
  // whose request has not answered yet.
  const set = (change: Partial<ProviderDraft>) => {
    setDraft((current) => ({...current, ...change}));
    if ("baseURL" in change || "anthropicBaseURL" in change || "key" in change || "removeKey" in change) {
      checkGeneration.current++;
      setCheck(null);
    }
  };
  // A problem shows once the user tried to save.
  const [tried, setTried] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  useEffect(() => {
    const checks = checkGeneration;
    form.current?.scrollIntoView({block: "nearest"});
    return () => {
      checks.current++;
    };
  }, []);
  const problem = draftProblem(draft, others);
  const submit = () => {
    setTried(true);
    if (problem || saving) return;
    onSave(draft);
  };
  const runCheck = async () => {
    const generation = ++checkGeneration.current;
    setCheck({state: "checking"});
    const key = draft.removeKey ? "" : draft.key.trim();
    try {
      const result = await api.checkProvider({
        id: provider?.id ?? draft.id.trim(),
        base_url: draft.baseURL.trim(),
        anthropic_base_url: draft.anthropicBaseURL.trim(),
        // Without a key typed, the hub checks with the one it keeps only at
        // unchanged endpoints.
        ...(key || draft.removeKey || !provider?.has_key ? {api_key: key} : {}),
      });
      if (checkGeneration.current === generation) setCheck({state: "ok", models: result.models, latency: result.latency_ms});
    } catch (error) {
      if (checkGeneration.current === generation) setCheck({state: "failed", reason: hubErrorText(error)});
    }
  };
  const picked = new Set(parseModelIDs(draft.models));
  const isNew = !provider;
  const canCheck = /^https?:\/\/[^/?#]/i.test(draft.baseURL.trim()) && check?.state !== "checking";
  return (
    <form
      ref={form}
      aria-label={isNew ? "New provider" : `Edit ${providerName(provider)}`}
      className="space-y-5"
      onSubmit={(event) => {
        event.preventDefault();
        submit();
      }}
      onKeyDown={(event) => {
        if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
          event.preventDefault();
          submit();
        }
      }}
    >
      <div className="flex items-center gap-3">
        <ProviderTile provider={{id: draft.id.trim() || "new", name: draft.name.trim() || draft.id.trim() || "New"}} className="size-9 rounded-lg text-xs" />
        <div className="min-w-0">
          <p className="truncate text-sm font-semibold">{isNew ? "New provider" : providerName(provider)}</p>
          <p className="text-xs text-muted-foreground">{isNew ? "An OpenAI- or Anthropic-compatible endpoint" : "Changes reach every machine within a minute"}</p>
        </div>
      </div>

      <div className="grid gap-3 sm:grid-cols-2">
        <Field label="ID" hint={isNew ? "Lowercase, - and _" : "Fixed once added"}>
          <input
            autoFocus={isNew}
            value={draft.id}
            disabled={!isNew}
            maxLength={63}
            onChange={(event) => set({id: event.target.value})}
            placeholder="gateway"
            spellCheck={false}
            autoCapitalize="off"
            aria-label="ID"
            className={cn(fieldClass, "font-mono disabled:opacity-60")}
          />
        </Field>
        <Field label="Name" hint="Optional">
          <input
            autoFocus={!isNew}
            value={draft.name}
            onChange={(event) => set({name: event.target.value})}
            placeholder={draft.id || "LiteLLM gateway"}
            aria-label="Name"
            className={fieldClass}
          />
        </Field>
      </div>

      <div className="space-y-3">
        <Field label="Base URL" note="Codex calls <base URL>/responses; Claude Code calls it without /v1, plus /v1/messages.">
          <input
            type="url"
            value={draft.baseURL}
            onChange={(event) => set({baseURL: event.target.value})}
            placeholder="https://gateway.example.com/v1"
            spellCheck={false}
            aria-label="Base URL"
            className={cn(fieldClass, "font-mono")}
          />
        </Field>
        <div className="space-y-1.5">
          <Field label="API key" hint={provider?.has_key ? undefined : "Optional"}>
            <span className="relative block">
              <input
                type={showKey ? "text" : "password"}
                autoComplete="new-password"
                value={draft.key}
                disabled={draft.removeKey}
                onChange={(event) => set({key: event.target.value})}
                placeholder={draft.removeKey ? "Removed on save" : provider?.has_key ? "Kept · type to replace" : "None"}
                spellCheck={false}
                aria-label="API key"
                className={cn(fieldClass, "pr-11 font-mono disabled:opacity-60")}
              />
              <button
                type="button"
                aria-label={showKey ? "Hide the key" : "Show the key"}
                aria-pressed={showKey}
                disabled={!draft.key}
                onClick={() => setShowKey((shown) => !shown)}
                className="absolute inset-y-0 right-1 my-auto grid size-9 place-items-center rounded-md text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/40 disabled:opacity-30"
              >
                {showKey ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
              </button>
            </span>
          </Field>
          <div className="flex flex-wrap items-center justify-between gap-2">
            <p className="text-xs text-muted-foreground">Sent to every joined machine; it can&apos;t be read back here.</p>
            {provider?.has_key && (
              <label className="flex items-center gap-2 text-xs">
                <input
                  type="checkbox"
                  checked={draft.removeKey}
                  onChange={(event) => set({removeKey: event.target.checked, key: ""})}
                  className="size-3.5 accent-destructive"
                />
                Remove the key
              </label>
            )}
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" variant="outline" size="sm" className="h-9" disabled={!canCheck} onClick={() => void runCheck()}>
            {check?.state === "checking" ? <LoaderCircle className="animate-spin" /> : <Plug />}
            Check connection
          </Button>
          <CheckStatus check={check} />
        </div>
      </div>

      <Field label="Models" hint="Optional" note="One ID per line or comma-separated. Empty offers what the provider lists at <base URL>/models.">
        <textarea
          value={draft.models}
          onChange={(event) => set({models: event.target.value})}
          rows={3}
          placeholder={"Kimi-K3-NVFP4\nGLM-5"}
          spellCheck={false}
          aria-label="Models"
          className={cn(fieldClass, "h-auto min-h-20 resize-y py-2.5 font-mono leading-6")}
        />
      </Field>
      {check?.state === "ok" && check.models.length > 0 && (
        <div className="-mt-2 space-y-2 rounded-xl bg-muted/50 p-3">
          <p className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>Tap to offer only some of them; none picked offers them all.</span>
            {picked.size > 0 && (
              <button type="button" className="shrink-0 font-medium text-foreground underline-offset-2 hover:underline" onClick={() => set({models: ""})}>
                Offer all
              </button>
            )}
          </p>
          <div role="group" aria-label="The provider's models" className="flex max-h-40 flex-wrap gap-1.5 overflow-y-auto">
            {check.models.map((model) => {
              const on = picked.has(model.id);
              return (
                <button
                  key={model.id}
                  type="button"
                  aria-pressed={on}
                  onClick={() => set({models: toggleModel(draft.models, model.id)})}
                  className={cn(
                    "inline-flex h-7 items-center gap-1 rounded-full border px-2.5 font-mono text-xs transition-colors outline-none focus-visible:ring-[3px] focus-visible:ring-ring/40",
                    on ? "border-foreground bg-foreground text-background" : "bg-background hover:border-foreground/40",
                  )}
                >
                  {on && <Check className="size-3" aria-hidden="true" />}
                  {model.name || model.id}
                </button>
              );
            })}
          </div>
        </div>
      )}

      <div className="rounded-xl border border-dashed">
        <button
          type="button"
          aria-expanded={advanced}
          onClick={() => setAdvanced((open) => !open)}
          className="flex w-full items-center gap-2 rounded-xl px-3 py-2.5 text-left text-xs font-medium text-muted-foreground outline-none hover:text-foreground focus-visible:ring-[3px] focus-visible:ring-ring/40"
        >
          <ChevronRight className={cn("size-3.5 transition-transform", advanced && "rotate-90")} aria-hidden="true" />
          Advanced
          {!advanced && draft.anthropicBaseURL && <span className="ml-auto font-mono font-normal">Anthropic URL set</span>}
        </button>
        {advanced && (
          <div className="px-3 pb-3">
            <Field label="Anthropic base URL" hint="Optional" note="For Claude Code, when its endpoint isn't the base URL without /v1.">
              <input
                type="url"
                value={draft.anthropicBaseURL}
                onChange={(event) => set({anthropicBaseURL: event.target.value})}
                placeholder={draft.baseURL.trim().replace(/\/+$/, "").replace(/\/v1$/, "") || "https://gateway.example.com"}
                spellCheck={false}
                aria-label="Anthropic base URL"
                className={cn(fieldClass, "font-mono")}
              />
            </Field>
          </div>
        )}
      </div>

      {tried && problem && (
        <p role="alert" className="flex items-center gap-2 text-sm text-destructive">
          <CircleAlert className="size-4 shrink-0" aria-hidden="true" />
          {problem}
        </p>
      )}
      <div className="flex items-center justify-end gap-2 border-t pt-4">
        <span className="mr-auto hidden text-xs text-muted-foreground sm:inline">
          <span className="inline-flex items-center gap-1 whitespace-nowrap">
            <Kbd small>⌘/Ctrl</Kbd>
            <Kbd small>Enter</Kbd>
            saves
          </span>
        </span>
        <Button
          type="button"
          variant="ghost"
          className="h-9"
          onClick={() => {
            checkGeneration.current++;
            onCancel();
          }}
        >
          Cancel
        </Button>
        <Button type="submit" className="h-9 min-w-20" disabled={saving || (tried && !!problem)}>
          {saving && <LoaderCircle className="animate-spin" />}
          {isNew ? "Add provider" : "Save"}
        </Button>
      </div>
    </form>
  );
}

// CheckStatus is the answer of the last connection check, announced.
function CheckStatus({check}: {check: Check | null}) {
  return (
    <p role="status" aria-live="polite" className="min-w-0 flex-1 text-xs">
      {check?.state === "checking" && <span className="text-muted-foreground">Asking the provider from the hub…</span>}
      {check?.state === "ok" && (
        <span className="inline-flex items-center gap-1.5 font-medium text-emerald-700 dark:text-emerald-400">
          <CircleCheck className="size-3.5 shrink-0" aria-hidden="true" />
          Reached in {check.latency} ms · {check.models.length === 1 ? "1 model" : `${check.models.length} models`}
        </span>
      )}
      {check?.state === "failed" && (
        <span className="inline-flex min-w-0 items-start gap-1.5 text-destructive">
          <CircleX className="mt-px size-3.5 shrink-0" aria-hidden="true" />
          <span className="line-clamp-2 break-words" title={check.reason}>
            {checkFailureText(check.reason)}
          </span>
        </span>
      )}
    </p>
  );
}

// Field is a labeled field, with a hint beside the label and a note under
// it.
function Field({label, hint, note, children}: {label: string; hint?: string; note?: string; children: React.ReactNode}) {
  return (
    <label className="block space-y-1.5">
      <span className="flex items-baseline justify-between gap-2 text-sm font-medium">
        {label}
        {hint && <span className="text-xs font-normal text-muted-foreground">{hint}</span>}
      </span>
      {children}
      {note && <span className="block text-xs text-muted-foreground">{note}</span>}
    </label>
  );
}
