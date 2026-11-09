import {formatUSDShort} from "@/lib/format";
import {cn} from "@/lib/utils";

// Money is an amount as formatUSDShort says it, with room around its decimal
// point: Geist sets the point so close after a 2 or a 3 that at small
// sizes "$3.19" reads as "$319". The text stays the same for copying and
// screen readers.
export function Money({usd, className}: {usd: number; className?: string}) {
  const text = formatUSDShort(usd);
  const point = text.indexOf(".");
  return (
    <span className={cn("tabular-nums", className)}>
      {point === -1 ? (
        text
      ) : (
        <>
          {text.slice(0, point)}
          <span className="mx-[0.06em]">.</span>
          {text.slice(point + 1)}
        </>
      )}
    </span>
  );
}
