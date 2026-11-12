"use client";

import {useEffect, useMemo, useRef, useState} from "react";
import {Cloud, KeyRound, LoaderCircle, Pencil, Plus, Trash2} from "lucide-react";
import {toast} from "sonner";
import {Button} from "@/components/ui/button";
import {createHubAPI, hubErrorText} from "@/lib/hub-api";
import {
  changeOf,
  draftOf,
  draftProblem,
  keptChange,
  maxProviders,
  providerModelsText,
  providerName,
  type Provider,
  type ProviderChange,
  type ProviderDraft,
} from "@/lib/hub-providers";
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
// editor first, and a click outside doesn't lose what it holds.
export function ProvidersSettings({
  root,
  editor,
  headingRef,
}: {
  root: string;
  editor: React.RefObject<{open: boolean; cancel: () => void} | null>;
  headingRef?: React.Ref<HTMLHeadingElement>;
}) {
  const api = useMemo(() => createHubAPI(root), [root]);
  const [providers, setProviders] = useState<Provider[] | undefined>(undefined);
  const [loadError, setLoadError] = useState("");
  const [editing, setEditing] = useState<Editing | null>(null);
  const [deleting, setDeleting] = useState<string | null>(null);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    let current = true;
    api.getProviders().then(
      (list) => current && setProviders(list),
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
        setEditing(null);
        setDeleting(null);
      },
    };
  });

  // save sends the whole list, and reports whether the hub kept it.
  const save = async (next: ProviderChange[]): Promise<boolean> => {
    setSaving(true);
    try {
      setProviders(await api.setProviders(next));
      return true;
    } catch (error) {
      toast.error("Couldn't save the providers", {description: hubErrorText(error)});
      return false;
    } finally {
      setSaving(false);
    }
  };

  const list = providers ?? [];
  // commit saves draft in place of previous, or after the others.
  const commit = async (draft: ProviderDraft, previous: Provider | undefined) => {
    const change = changeOf(draft, previous);
    const next = previous ? list.map((each) => (each.id === previous.id ? change : keptChange(each))) : [...list.map(keptChange), change];
    if (await save(next)) setEditing(null);
  };
  const remove = async (provider: Provider) => {
    if (await save(list.filter((each) => each.id !== provider.id).map(keptChange))) setDeleting(null);
  };

  const full = list.length >= maxProviders;
  const adding = editing?.id === null;
  const startAdding = () => {
    setDeleting(null);
    setEditing({id: null});
  };
  return (
    <section aria-labelledby="hub-settings-providers" className="space-y-3">
      <div className="flex items-center gap-3">
        <div className="min-w-0 flex-1">
          <h3 id="hub-settings-providers" ref={headingRef} tabIndex={-1} className="flex items-baseline gap-2 text-base font-semibold outline-none">
            Providers
            {list.length > 0 && (
              <span className="text-xs font-normal text-muted-foreground tabular-nums">
                {list.length}/{maxProviders}
              </span>
            )}
          </h3>
          <p className="text-sm text-muted-foreground">
            OpenAI- or Anthropic-compatible endpoints, such as a LiteLLM gateway, that Claude Code and Codex sessions can switch to. The AgentAPIs that report to
            this hub get them within a minute, keys included, so every machine joined to it can use them. A provider with the same ID in a machine&apos;s
            ~/.agentapi/config.yaml takes precedence there.
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

      {providers === undefined &&
        (loadError ? (
          <p role="alert" className="text-sm text-destructive">
            Couldn&apos;t read the providers: {loadError}
          </p>
        ) : (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ))}

      {providers !== undefined && list.length === 0 && !adding && (
        <div className="flex flex-col items-center gap-2 rounded-xl border border-dashed px-6 py-8 text-center">
          <Cloud className="size-6 text-muted-foreground" aria-hidden="true" />
          <p className="text-sm font-medium">No providers yet</p>
          <p className="max-w-sm text-sm text-muted-foreground">Add a gateway once here, and the sessions on every machine joined to the hub can switch to it.</p>
          <Button type="button" size="sm" className="mt-2 h-9" onClick={startAdding}>
            <Plus /> Add a provider
          </Button>
        </div>
      )}

      {list.length > 0 && (
        <ul aria-label="Providers" className="divide-y overflow-hidden rounded-xl border bg-card">
          {list.map((provider) =>
            editing?.id === provider.id ? (
              <li key={provider.id} className="p-3">
                <ProviderEditor
                  provider={provider}
                  others={list.filter((each) => each.id !== provider.id).map((each) => each.id)}
                  saving={saving}
                  onSave={(draft) => void commit(draft, provider)}
                  onCancel={() => setEditing(null)}
                />
              </li>
            ) : deleting === provider.id ? (
              <li key={provider.id} role="group" aria-label={`Delete ${providerName(provider)}`} className="flex items-center gap-2 py-1.5 pr-1.5 pl-3">
                <p className="min-w-0 flex-1 text-sm">
                  Delete “{providerName(provider)}”{provider.has_key && " and its key"}?
                </p>
                <Button type="button" variant="ghost" size="sm" className="h-8 shrink-0" onClick={() => setDeleting(null)}>
                  Cancel
                </Button>
                <Button autoFocus type="button" variant="destructive" size="sm" className="h-8 shrink-0" disabled={saving} onClick={() => void remove(provider)}>
                  {saving && <LoaderCircle className="animate-spin" />}
                  Delete
                </Button>
              </li>
            ) : (
              <li key={provider.id} className="flex items-center gap-1 py-1.5 pr-1.5 pl-3" data-provider={provider.id}>
                <div className="min-w-0 flex-1 py-1">
                  <p className="flex items-baseline gap-2 truncate text-sm font-medium">
                    {providerName(provider)}
                    {provider.name && <span className="font-mono text-xs font-normal text-muted-foreground">{provider.id}</span>}
                  </p>
                  <p className="truncate text-xs text-muted-foreground">
                    {provider.base_url} · {providerModelsText(provider)}
                  </p>
                </div>
                <div className="flex shrink-0 items-center">
                  <span role="img" aria-label={provider.has_key ? "Has a key" : "No key"} title={provider.has_key ? "Has a key" : "No key"} className="mx-2 inline-flex">
                    <KeyRound className={cn("size-3.5", provider.has_key ? "text-foreground" : "text-muted-foreground/40")} aria-hidden="true" />
                  </span>
                  <IconButton
                    label={`Edit ${providerName(provider)}`}
                    onClick={() => {
                      setDeleting(null);
                      setEditing({id: provider.id});
                    }}
                  >
                    <Pencil />
                  </IconButton>
                  <IconButton label={`Delete ${providerName(provider)}`} className="hover:text-destructive" onClick={() => setDeleting(provider.id)}>
                    <Trash2 />
                  </IconButton>
                </div>
              </li>
            ),
          )}
        </ul>
      )}

      {adding && (
        <div className="rounded-xl border bg-card p-3">
          <ProviderEditor others={list.map((each) => each.id)} saving={saving} onSave={(draft) => void commit(draft, undefined)} onCancel={() => setEditing(null)} />
        </div>
      )}
    </section>
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

// ProviderEditor edits a provider, or a new one without provider. Its ID is
// fixed once it is added: the hub keeps a key by ID, and sessions name the
// provider by it. The key is write-only: empty keeps the one it has.
// ⌘/Ctrl+Enter saves.
function ProviderEditor({
  provider,
  others,
  saving,
  onSave,
  onCancel,
}: {
  provider?: Provider;
  others: readonly string[];
  saving: boolean;
  onSave: (draft: ProviderDraft) => void;
  onCancel: () => void;
}) {
  const [draft, setDraft] = useState(() => draftOf(provider));
  const set = (change: Partial<ProviderDraft>) => setDraft((current) => ({...current, ...change}));
  // A problem shows once the user tried to save.
  const [tried, setTried] = useState(false);
  const form = useRef<HTMLFormElement>(null);
  useEffect(() => {
    form.current?.scrollIntoView({block: "nearest"});
  }, []);
  const problem = draftProblem(draft, others);
  const submit = () => {
    setTried(true);
    if (problem || saving) return;
    onSave(draft);
  };
  const isNew = !provider;
  return (
    <form
      ref={form}
      aria-label={isNew ? "New provider" : `Edit ${providerName(provider)}`}
      className="space-y-3"
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
        <Field label="Name" hint="Optional · the ID without one">
          <input
            autoFocus={!isNew}
            value={draft.name}
            onChange={(event) => set({name: event.target.value})}
            placeholder={draft.id || "LiteLLM"}
            aria-label="Name"
            className={fieldClass}
          />
        </Field>
      </div>
      <Field label="Base URL" note="Codex calls <base URL>/responses; Claude Code calls the base URL without /v1, plus /v1/messages.">
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
      <Field label="Anthropic base URL" hint="Optional" note="For Claude Code, when it isn't the base URL without /v1.">
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
      <Field label="Models" hint="Optional" note="One model ID per line, or comma-separated. Empty uses those the provider lists at <base URL>/models.">
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
      <div className="space-y-1.5">
        <Field label="API key" hint={provider?.has_key ? undefined : "Optional"} note="Sent to the AgentAPIs that report to the hub. It can't be read back.">
          <input
            type="password"
            autoComplete="new-password"
            value={draft.key}
            disabled={draft.removeKey}
            onChange={(event) => set({key: event.target.value})}
            placeholder={provider?.has_key ? "Set: type to replace it" : "None"}
            aria-label="API key"
            className={cn(fieldClass, "font-mono disabled:opacity-60")}
          />
        </Field>
        {provider?.has_key && (
          <label className="flex items-center gap-2 text-xs">
            <input
              type="checkbox"
              checked={draft.removeKey}
              onChange={(event) => set({removeKey: event.target.checked, key: ""})}
              className="size-3.5 accent-primary"
            />
            Remove the key
          </label>
        )}
      </div>
      {tried && problem && (
        <p role="alert" className="text-sm text-destructive">
          {problem}
        </p>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" className="h-9" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" className="h-9" disabled={saving || (tried && !!problem)}>
          {saving && <LoaderCircle className="animate-spin" />}
          {isNew ? "Add" : "Save"}
        </Button>
      </div>
    </form>
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
