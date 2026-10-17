// Keyboard handling shared by the composer and the decision cards.

// A key press as React (nativeEvent) or a window listener reports it.
export interface ImeKeyEvent {
  keyCode: number;
  isComposing?: boolean;
  nativeEvent?: {isComposing: boolean};
}

// Whether a key press belongs to an IME composition, such as the Enter that
// picks a candidate while typing Chinese or Japanese: it must not send,
// submit or pick anything. Safari fires that Enter after compositionend, so
// isComposing is false there; keyCode 229 marks it.
export function isImeComposing(event: ImeKeyEvent): boolean {
  return Boolean(event.nativeEvent?.isComposing ?? event.isComposing) || event.keyCode === 229;
}

// Where a number key means something else: text fields, the terminal (TTY
// mode), and dialogs, menus and lists (the Explorer, the shortcuts dialog,
// the send menu), which take keys for themselves.
const OWN_KEYS = '[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"],.xterm';

interface KeyTarget {
  tagName: string;
  isContentEditable: boolean;
  closest(selector: string): unknown;
}

function takesKeys(target: unknown): boolean {
  if (!target || typeof (target as KeyTarget).closest !== "function") return false;
  const element = target as KeyTarget;
  return element.isContentEditable || /^(INPUT|TEXTAREA|SELECT)$/.test(element.tagName) || Boolean(element.closest(OWN_KEYS));
}

export interface NumberKeyEvent extends ImeKeyEvent {
  key: string;
  metaKey: boolean;
  ctrlKey: boolean;
  altKey: boolean;
  repeat: boolean;
  defaultPrevented: boolean;
  target: unknown;
}

// The option a number key picks on a decision card (0 for "1"), or null
// when the key isn't meant for the card. A held key doesn't repeat: on a
// multiple-choice question that would toggle the option back and forth.
export function numberKeyIndex(event: NumberKeyEvent, count: number): number | null {
  if (event.defaultPrevented || event.repeat || isImeComposing(event)) return null;
  if (event.metaKey || event.ctrlKey || event.altKey || !/^[1-9]$/.test(event.key)) return null;
  const index = Number(event.key) - 1;
  if (index >= count || takesKeys(event.target)) return null;
  return index;
}
