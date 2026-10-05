import { CODEX_MARK, PI_MARK, hasLogo, squareTint } from "@/lib/agent-mark";

// The agent's mark: its logo when it has one, otherwise a tinted square.
// A logo has more detail than a square, so it is drawn a little larger.
export function AgentMark({ agentType, className }: { agentType: string; className?: string }) {
  if (agentType === "codex") {
    return (
      <svg aria-hidden="true" viewBox={CODEX_MARK.viewBox} className={`shrink-0 text-foreground ${className ?? "size-3.5"}`} data-agent-mark={agentType}>
        <path fill="currentColor" d={CODEX_MARK.d} />
      </svg>
    );
  }
  if (hasLogo(agentType)) {
    return (
      <svg aria-hidden="true" viewBox={PI_MARK.viewBox} className={`shrink-0 ${className ?? "size-3.5"}`} data-agent-mark={agentType}>
        {PI_MARK.paths.map((path) => (
          <path key={path.d} fill={path.fill} d={path.d} />
        ))}
      </svg>
    );
  }
  return (
    <span
      aria-hidden="true"
      className={`shrink-0 rounded-[3px] ${className ?? "size-2.5"}`}
      data-agent-mark={agentType}
      style={{ background: squareTint(agentType).css }}
    />
  );
}
