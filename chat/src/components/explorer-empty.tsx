// Empty is what an Explorer tab shows when it has nothing to list.
export function Empty({text}: {text: string}) {
  return <p className="rounded-xl border border-dashed p-8 text-center text-sm text-muted-foreground">{text}</p>;
}
