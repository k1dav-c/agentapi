"use client"

import { useTheme } from "next-themes"
import { Toaster as Sonner, toast, useSonner, type ToastT, ToasterProps } from "sonner"

// The toasts' gap from the bottom of the screen (Sonner's defaults; its
// phone layout is up to 600px wide), and the room the Clear all button
// takes below them.
const EDGE = 24
const MOBILE_EDGE = 16
const CLEAR_ROOM = 44

// clearToasts closes every toast as its close button would: each one's
// onDismiss runs (Sonner's dismiss() alone skips it, and a toast that
// remembers being closed would come back after a reload), then all go.
export function clearToasts(toasts: Pick<ToastT, "onDismiss">[], dismiss: () => void = () => void toast.dismiss()) {
  for (const shown of toasts) shown.onDismiss?.(shown as ToastT)
  dismiss()
}

// ClearAll closes the toasts at once, below them while two or more show.
function ClearAll({ toasts }: { toasts: ToastT[] }) {
  return (
    <button
      type="button"
      data-toasts-clear
      onClick={() => clearToasts(toasts)}
      className="fixed right-4 bottom-4 left-4 z-[1000000000] h-9 rounded-lg border bg-popover text-sm font-medium text-popover-foreground shadow-md transition-colors outline-none hover:bg-accent focus-visible:ring-[3px] focus-visible:ring-ring/50 min-[601px]:left-auto min-[601px]:right-6 min-[601px]:bottom-6 min-[601px]:w-[356px]"
    >
      Clear all ({toasts.length})
    </button>
  )
}

const Toaster = ({ ...props }: ToasterProps) => {
  const { theme = "system" } = useTheme()
  const { toasts } = useSonner()
  const many = toasts.length > 1

  return (
    <>
      <Sonner
        theme={theme as ToasterProps["theme"]}
        className="toaster group"
        style={
          {
            "--normal-bg": "var(--popover)",
            "--normal-text": "var(--popover-foreground)",
            "--normal-border": "var(--border)",
          } as React.CSSProperties
        }
        offset={many ? { bottom: EDGE + CLEAR_ROOM } : undefined}
        mobileOffset={many ? { bottom: MOBILE_EDGE + CLEAR_ROOM } : undefined}
        {...props}
      />
      {many && <ClearAll toasts={toasts} />}
    </>
  )
}

export { Toaster }
