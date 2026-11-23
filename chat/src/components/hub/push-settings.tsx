"use client";

import {useCallback, useEffect, useState} from "react";
import {toast} from "sonner";
import {Moon, Pin, Smartphone, Trash2} from "lucide-react";
import {Button} from "@/components/ui/button";
import {hubErrorText} from "@/lib/hub-api";
import {formatTimeAndDay} from "@/lib/hub-format";
import type {PushDevice, PushPrefsChange} from "@/lib/hub-push";
import {cn} from "@/lib/utils";
import type {HubPushState} from "./use-push";

// The quiet hours a device gets when they are turned on, in the browser's
// time zone.
const defaultQuiet = {start: "22:00", end: "08:00"};

// PushSettings is the settings' Push section: the devices the hub pushes
// to, what each gets, and removing one. groups are the hub's group names.
export function PushSettings({push, groups, headingRef}: {push: HubPushState; groups: string[]; headingRef?: React.Ref<HTMLHeadingElement>}) {
  const [devices, setDevices] = useState<PushDevice[] | undefined>(undefined);
  const [error, setError] = useState("");
  const [thisDevice, setThisDevice] = useState<string | undefined>(undefined);
  const {api} = push;
  const read = useCallback(async () => {
    try {
      const [list, id] = await Promise.all([api.devices(), api.thisDevice().catch(() => undefined)]);
      setDevices(list);
      setThisDevice(id);
      setError("");
    } catch (failed) {
      setError(hubErrorText(failed));
    }
  }, [api]);
  useEffect(() => {
    void read();
  }, [read, push.push]);

  const change = async (id: string, prefs: PushPrefsChange) => {
    try {
      const changed = await api.change(id, prefs);
      setDevices((all) => all?.map((device) => (device.id === id ? changed : device)));
    } catch (failed) {
      toast.error("Couldn't change the device", {description: hubErrorText(failed)});
    }
  };
  const remove = async (id: string) => {
    try {
      // This browser turns push off; another stops at its next visit (the
      // hub refuses to renew it). Read again: the hub says what stayed.
      if (id === thisDevice && push.push) await push.togglePush();
      else await api.remove(id);
      await read();
    } catch (failed) {
      toast.error("Couldn't remove the device", {description: hubErrorText(failed)});
    }
  };

  return (
    <PushSettingsView
      devices={devices}
      error={error}
      thisDevice={thisDevice}
      pushHere={push.push}
      groups={groups}
      headingRef={headingRef}
      onRetry={() => void read()}
      onPushHere={() => void push.togglePush()}
      onChange={(id, prefs) => void change(id, prefs)}
      onRemove={(id) => void remove(id)}
    />
  );
}

// PushSettingsView draws the section from what was read.
export function PushSettingsView({
  devices,
  error,
  thisDevice,
  pushHere,
  groups,
  headingRef,
  now = Date.now(),
  onRetry,
  onPushHere,
  onChange,
  onRemove,
}: {
  devices: PushDevice[] | undefined;
  error: string;
  thisDevice: string | undefined;
  pushHere: boolean;
  groups: string[];
  headingRef?: React.Ref<HTMLHeadingElement>;
  now?: number;
  onRetry: () => void;
  onPushHere: () => void;
  onChange: (id: string, prefs: PushPrefsChange) => void;
  onRemove: (id: string) => void;
}) {
  // This device first.
  const sorted = devices && [...devices].sort((a, b) => Number(b.id === thisDevice) - Number(a.id === thisDevice));
  return (
    <section aria-labelledby="hub-settings-push" aria-busy={devices === undefined && !error} className="space-y-3">
      <div>
        <h3 id="hub-settings-push" ref={headingRef} tabIndex={-1} className="text-base font-semibold outline-none">
          Push
        </h3>
        <p className="text-sm text-muted-foreground">
          The devices the hub pushes to, even with the page closed, and what each gets. A device stops getting pushes when its sign-in ends.
        </p>
      </div>
      {error && (
        <p role="alert" className="flex items-center gap-2 text-sm text-destructive">
          {error}
          <Button variant="outline" size="sm" onClick={onRetry}>
            Try again
          </Button>
        </p>
      )}
      {sorted?.length === 0 && <p className="text-sm text-muted-foreground">No device gets pushes yet.</p>}
      {!pushHere && devices !== undefined && (
        <Button variant="outline" size="sm" onClick={onPushHere}>
          <Smartphone /> Push to this device
        </Button>
      )}
      <ul aria-label="Devices" className="space-y-3">
        {sorted?.map((device) => (
          <DeviceCard key={device.id} device={device} here={device.id === thisDevice} groups={groups} now={now} onChange={(prefs) => onChange(device.id, prefs)} onRemove={() => onRemove(device.id)} />
        ))}
      </ul>
    </section>
  );
}

function DeviceCard({device, here, groups, now, onChange, onRemove}: {device: PushDevice; here: boolean; groups: string[]; now: number; onChange: (prefs: PushPrefsChange) => void; onRemove: () => void}) {
  const name = device.label || "A browser";
  const quiet = device.quiet_hours;
  // The groups it can follow: the hub's, those it follows that are gone,
  // and the ungrouped agents.
  const choices = [...new Set([...groups, ...device.groups.filter((group) => group !== "")])].sort((a, b) => a.localeCompare(b));
  const following = new Set(device.groups);
  const all = following.size === 0 && !device.pinned;
  const toggleGroup = (group: string) => {
    const next = new Set(following);
    if (next.has(group)) next.delete(group);
    else next.add(group);
    onChange({groups: [...next]});
  };
  return (
    <li aria-label={name} className="space-y-3 rounded-xl border p-3">
      <div className="flex items-start gap-2">
        <Smartphone aria-hidden="true" className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
        <div className="min-w-0 flex-1">
          <p className="flex items-center gap-2 text-sm font-medium">
            {name}
            {here && <span className="rounded-full bg-muted px-2 py-0.5 text-[11px] font-medium text-muted-foreground">This device</span>}
          </p>
          <p className="text-xs text-muted-foreground">{deviceStatus(device, now)}</p>
          {device.last_error && <p className="text-xs text-destructive">Last push failed: {device.last_error}</p>}
        </div>
        <Button variant="ghost" size="icon" className="size-8 text-muted-foreground" aria-label={`Remove ${name}`} title="Stop pushing to this device" onClick={onRemove}>
          <Trash2 className="size-4" />
        </Button>
      </div>
      <fieldset className="space-y-1.5">
        <legend className="sr-only">What it gets</legend>
        <p className="text-xs text-muted-foreground">What needs you, and:</p>
        <Check checked={device.finished} onChange={(finished) => onChange({finished})} label="Finished tasks" />
        <Check checked={device.issues} onChange={(issues) => onChange({issues})} label="Issues: a pull request opened, a job failed or done" />
      </fieldset>
      <fieldset className="space-y-1.5">
        <legend className="text-xs text-muted-foreground">Agents {all ? "(all of them)" : "(only these)"}</legend>
        <div className="flex flex-wrap gap-1.5">
          <Chip pressed={device.pinned} onClick={() => onChange({pinned: !device.pinned})}>
            <Pin className="size-3" /> Pinned
          </Chip>
          {choices.map((group) => (
            <Chip key={group} pressed={following.has(group)} onClick={() => toggleGroup(group)}>
              {group}
            </Chip>
          ))}
          <Chip pressed={following.has("")} onClick={() => toggleGroup("")}>
            Ungrouped
          </Chip>
        </div>
      </fieldset>
      <div className="space-y-1.5">
        <Check
          checked={!!quiet}
          onChange={(on) =>
            onChange({quiet_hours: on ? {...defaultQuiet, time_zone: Intl.DateTimeFormat().resolvedOptions().timeZone} : ({} as PushPrefsChange["quiet_hours"])})
          }
          label={
            <>
              <Moon className="size-3.5" /> Quiet hours
            </>
          }
        />
        {quiet && (
          <div className="flex flex-wrap items-center gap-2 pl-6 text-sm">
            <input
              type="time"
              aria-label="Quiet from"
              defaultValue={quiet.start}
              onBlur={(event) => event.target.value && event.target.value !== quiet.start && onChange({quiet_hours: {...quiet, start: event.target.value}})}
              className="h-8 rounded-md border bg-background px-2"
            />
            <span className="text-muted-foreground">to</span>
            <input
              type="time"
              aria-label="Quiet until"
              defaultValue={quiet.end}
              onBlur={(event) => event.target.value && event.target.value !== quiet.end && onChange({quiet_hours: {...quiet, end: event.target.value}})}
              className="h-8 rounded-md border bg-background px-2"
            />
            <span className="text-xs text-muted-foreground">{quiet.time_zone}</span>
          </div>
        )}
      </div>
    </li>
  );
}

// deviceStatus says when the device last got a push and when it stops.
export function deviceStatus(device: PushDevice, now: number): string {
  const parts = [device.last_sent_at ? `Last push ${formatTimeAndDay(device.last_sent_at, now)}` : "No push yet"];
  if (device.expires_at) parts.push(`signed in until ${formatTimeAndDay(device.expires_at, now)}`);
  return parts.join(" · ");
}

function Check({checked, onChange, label}: {checked: boolean; onChange: (checked: boolean) => void; label: React.ReactNode}) {
  return (
    <label className="flex cursor-pointer items-center gap-2 text-sm">
      <input type="checkbox" checked={checked} onChange={(event) => onChange(event.target.checked)} className="size-4 accent-foreground" />
      <span className="inline-flex items-center gap-1.5">{label}</span>
    </label>
  );
}

function Chip({pressed, onClick, children}: {pressed: boolean; onClick: () => void; children: React.ReactNode}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      onClick={onClick}
      className={cn(
        "inline-flex h-7 items-center gap-1 rounded-full border px-2.5 text-xs font-medium outline-none transition-colors focus-visible:ring-[3px] focus-visible:ring-ring/40",
        pressed ? "border-foreground bg-foreground text-background" : "text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}
