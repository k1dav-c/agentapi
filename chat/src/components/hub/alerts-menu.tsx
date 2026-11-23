"use client";

import {Bell, BellOff, Send, SlidersHorizontal, Smartphone, Volume2} from "lucide-react";
import {Button} from "@/components/ui/button";
import {DropdownMenu, DropdownMenuCheckboxItem, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuSeparator, DropdownMenuTrigger} from "@/components/ui/dropdown-menu";
import type {HubAlerts} from "./use-hub-alerts";

// AlertsMenu is the bell of the hub's header: how the hub tells you an
// agent needs you while you look away. onPushSettings opens the settings
// at the devices the hub pushes to.
export function AlertsMenu({alerts, onPushSettings}: {alerts: HubAlerts; onPushSettings: () => void}) {
  const {notify, toggleNotify, sound, toggleSound, push} = alerts;
  const on = notify || sound || push.push;
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button
          variant="ghost"
          size="icon"
          className="size-9 rounded-full text-muted-foreground"
          aria-label={on ? "Alerts on" : "Alerts off"}
          title="Alerts when an agent needs you"
        >
          {on ? <Bell className="size-4 text-foreground" /> : <BellOff className="size-4" />}
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="w-64">
        <DropdownMenuLabel className="text-xs font-normal text-muted-foreground">When an agent needs you</DropdownMenuLabel>
        <DropdownMenuCheckboxItem checked={notify} onCheckedChange={() => void toggleNotify()} onSelect={(event) => event.preventDefault()}>
          <Bell /> Notifications
        </DropdownMenuCheckboxItem>
        <DropdownMenuCheckboxItem checked={sound} onCheckedChange={toggleSound} onSelect={(event) => event.preventDefault()}>
          <Volume2 /> Sound
        </DropdownMenuCheckboxItem>
        <DropdownMenuSeparator />
        {/* The hub pushes to this device with the page closed. */}
        <DropdownMenuCheckboxItem checked={push.push} onCheckedChange={() => void push.togglePush()} onSelect={(event) => event.preventDefault()}>
          <Smartphone /> Push to this device
        </DropdownMenuCheckboxItem>
        {push.push && (
          <DropdownMenuItem inset onSelect={() => void push.testPush()}>
            <Send /> Send a test
          </DropdownMenuItem>
        )}
        {/* What each device gets: tasks, issues, quiet hours, groups. */}
        <DropdownMenuItem inset onSelect={onPushSettings}>
          <SlidersHorizontal /> Push settings…
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
