"use client";

import {useCallback, useEffect, useState} from "react";
import {CircleCheck, CircleX, LoaderCircle, Send} from "lucide-react";
import {toast} from "sonner";
import {useChatActions} from "./chat-provider";
import {Button} from "./ui/button";
import type {WebhookChange, WebhookConfig, WebhookTest} from "@/lib/chat-api";
import {getErrorMessage} from "@/lib/error-utils";
import {WEBHOOK_EVENT_GROUPS, otherPatterns, patternsFor, selectedGroups} from "@/lib/webhook-events";

const inputClass = "h-8 w-full rounded-md border bg-card px-2.5 text-sm outline-none focus:ring-2 focus:ring-ring";

interface Form {
  url: string;
  groups: Set<string>;
  // Patterns the groups don't cover, kept as they are.
  others: string[];
  // A new secret; empty keeps the one set.
  secret: string;
  removeSecret: boolean;
  heartbeatSeconds: string;
  instanceID: string;
  publicURL: string;
  timeoutSeconds: string;
  maxAttempts: string;
  template: string;
}

function formOf(config: WebhookConfig): Form {
  const events = config.events ?? [];
  return {
    url: config.url,
    groups: selectedGroups(events),
    others: otherPatterns(events),
    secret: "",
    removeSecret: false,
    heartbeatSeconds: String(config.heartbeat_seconds),
    instanceID: config.instance_id,
    publicURL: config.public_url,
    timeoutSeconds: String(config.timeout_seconds),
    maxAttempts: String(config.max_attempts),
    template: config.payload_template,
  };
}

// The webhook that pushes what happens to a receiver, such as a dashboard
// that watches many agents (PUT /webhook): set here when AgentAPI started
// without one. active says the tab shows; it loads then.
export function WebhookSettings({active}: {active: boolean}) {
  const {getWebhook, updateWebhook, testWebhook} = useChatActions();
  const [config, setConfig] = useState<WebhookConfig | null>(null);
  const [form, setForm] = useState<Form | null>(null);
  const [error, setError] = useState("");
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [test, setTest] = useState<WebhookTest | null>(null);

  const load = useCallback(async () => {
    setError("");
    try {
      const next = await getWebhook();
      setConfig(next);
      setForm(formOf(next));
    } catch (failure) {
      setError(getErrorMessage(failure));
    }
  }, [getWebhook]);

  useEffect(() => {
    if (active) void load();
  }, [active, load]);

  if (!form || !config) {
    return error ? (
      <p role="alert" className="text-sm text-destructive">{error}</p>
    ) : (
      <p className="flex items-center gap-2 text-sm text-muted-foreground">
        <LoaderCircle className="size-4 animate-spin" /> Loading the webhook…
      </p>
    );
  }

  const set = (change: Partial<Form>) => setForm({...form, ...change});
  const toggle = (id: string) => {
    const groups = new Set(form.groups);
    if (groups.has(id)) groups.delete(id);
    else groups.add(id);
    set({groups});
  };
  const events = patternsFor(form.groups, form.others);
  const noEvents = form.url.trim() !== "" && events.length === 0;

  const save = async (disable = false) => {
    const change: WebhookChange = {
      url: disable ? "" : form.url.trim(),
      timeout_seconds: Number(form.timeoutSeconds) || config.timeout_seconds,
      max_attempts: Number(form.maxAttempts) || config.max_attempts,
      events,
      heartbeat_seconds: Math.max(0, Math.round(Number(form.heartbeatSeconds) || 0)),
      public_url: form.publicURL.trim(),
      payload_template: form.template,
    };
    if (form.instanceID.trim() !== config.instance_id) change.instance_id = form.instanceID.trim();
    if (form.removeSecret) change.secret = "";
    else if (form.secret) change.secret = form.secret;
    setSaving(true);
    try {
      const next = await updateWebhook(change);
      setConfig(next);
      setForm(formOf(next));
      setTest(null);
      toast.success(next.url ? "Webhook saved" : "Webhook turned off");
    } catch (failure) {
      toast.error("Couldn't save the webhook", {description: getErrorMessage(failure)});
    } finally {
      setSaving(false);
    }
  };

  const sendTest = async () => {
    setTesting(true);
    try {
      setTest(await testWebhook());
    } catch (failure) {
      setTest({ok: false, error: getErrorMessage(failure)});
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="space-y-4" data-webhook-settings>
      <div>
        <h3 className="text-sm font-semibold">Webhook</h3>
        <p className="mt-1 text-sm text-muted-foreground">
          Push what happens here to a receiver, such as a dashboard that watches many agents: AgentAPI POSTs a JSON event for
          each change. Saved settings apply at once and last until AgentAPI restarts, or with --state-db
          after it too, unless it starts with other webhook flags.
        </p>
      </div>

      <label className="block space-y-1">
        <span className="text-xs font-medium">Receiver URL</span>
        <input
          type="url"
          value={form.url}
          onChange={(event) => set({url: event.target.value})}
          placeholder="https://dashboard.example.com/agentapi/events"
          className={inputClass}
        />
      </label>

      <fieldset className="space-y-2">
        <legend className="text-xs font-medium">Events</legend>
        <div className="grid gap-2 sm:grid-cols-2">
          {WEBHOOK_EVENT_GROUPS.map((group) => (
            <label key={group.id} className="flex items-start gap-2.5 rounded-xl border p-2.5" data-webhook-group={group.id}>
              <input
                type="checkbox"
                checked={form.groups.has(group.id)}
                onChange={() => toggle(group.id)}
                className="mt-0.5 size-4 shrink-0 accent-primary"
              />
              <span>
                <span className="block text-sm font-medium">{group.label}</span>
                <span className="block text-xs text-muted-foreground">{group.detail}</span>
              </span>
            </label>
          ))}
        </div>
        {form.others.length > 0 && (
          <p className="text-xs text-muted-foreground">
            Also sent: <span className="font-mono">{form.others.join(", ")}</span>
          </p>
        )}
        {noEvents && (
          <p className="text-xs text-state-needs">Choose at least one kind of event: with none, only the run status is sent.</p>
        )}
      </fieldset>

      <label className="block space-y-1">
        <span className="text-xs font-medium">Signing secret</span>
        <input
          type="password"
          autoComplete="new-password"
          value={form.secret}
          disabled={form.removeSecret}
          onChange={(event) => set({secret: event.target.value})}
          placeholder={config.secret_set ? "Set: type to replace it" : "None: deliveries aren't signed"}
          className={inputClass}
        />
        <span className="block text-xs text-muted-foreground">
          Signs each delivery with HMAC-SHA256 in X-AgentAPI-Signature-256. It can&apos;t be read back.
        </span>
        {config.secret_set && (
          <span className="flex items-center gap-2 text-xs">
            <input
              type="checkbox"
              checked={form.removeSecret}
              onChange={(event) => set({removeSecret: event.target.checked, secret: ""})}
              className="size-3.5 accent-primary"
            />
            Remove the secret
          </span>
        )}
      </label>

      <div className="grid gap-3 sm:grid-cols-2">
        <label className="block space-y-1">
          <span className="text-xs font-medium">Instance ID</span>
          <input value={form.instanceID} onChange={(event) => set({instanceID: event.target.value})} className={inputClass} />
        </label>
        <label className="block space-y-1">
          <span className="text-xs font-medium">Heartbeat (seconds, 0 off)</span>
          <input
            type="number"
            min={0}
            value={form.heartbeatSeconds}
            onChange={(event) => set({heartbeatSeconds: event.target.value})}
            className={inputClass}
          />
        </label>
      </div>
      <label className="block space-y-1">
        <span className="text-xs font-medium">Public URL of this AgentAPI</span>
        <input
          type="url"
          value={form.publicURL}
          onChange={(event) => set({publicURL: event.target.value})}
          placeholder="For the receiver to link to this chat"
          className={inputClass}
        />
      </label>

      <details className="rounded-xl border p-3">
        <summary className="cursor-pointer text-xs font-medium">Delivery and payload</summary>
        <div className="mt-3 space-y-3">
          <div className="grid gap-3 sm:grid-cols-2">
            <label className="block space-y-1">
              <span className="text-xs font-medium">Timeout (seconds)</span>
              <input
                type="number"
                min={1}
                max={3600}
                value={form.timeoutSeconds}
                onChange={(event) => set({timeoutSeconds: event.target.value})}
                className={inputClass}
              />
            </label>
            <label className="block space-y-1">
              <span className="text-xs font-medium">Attempts</span>
              <input
                type="number"
                min={1}
                max={10}
                value={form.maxAttempts}
                onChange={(event) => set({maxAttempts: event.target.value})}
                className={inputClass}
              />
            </label>
          </div>
          <label className="block space-y-1">
            <span className="text-xs font-medium">Payload template (Go text/template; empty sends the JSON event)</span>
            <textarea
              value={form.template}
              onChange={(event) => set({template: event.target.value})}
              spellCheck={false}
              placeholder={'{"text":"{{.Instance.ID}}: {{.Type}}"}'}
              className="min-h-20 w-full resize-y rounded-md border bg-card p-2.5 font-mono text-xs outline-none focus:ring-2 focus:ring-ring"
            />
          </label>
        </div>
      </details>

      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" disabled={saving || noEvents} onClick={() => void save()}>
          {saving && <LoaderCircle className="animate-spin" />}
          Save
        </Button>
        <Button type="button" size="sm" variant="outline" disabled={testing || !config.url} onClick={() => void sendTest()} title="Send a webhook.test event to the saved URL">
          {testing ? <LoaderCircle className="animate-spin" /> : <Send />}
          Send test event
        </Button>
        {config.url && (
          <Button type="button" size="sm" variant="ghost" disabled={saving} onClick={() => void save(true)}>
            Turn off
          </Button>
        )}
      </div>
      {test && (
        <p
          role="status"
          className={`flex items-start gap-1.5 text-xs ${test.ok ? "text-state-ready" : "text-destructive"}`}
          data-webhook-test={test.ok ? "ok" : "failed"}
        >
          {test.ok ? <CircleCheck className="mt-px size-3.5 shrink-0" /> : <CircleX className="mt-px size-3.5 shrink-0" />}
          {test.ok ? `The receiver answered ${test.status}.` : `Not delivered: ${test.error}`}
        </p>
      )}
    </div>
  );
}
