import {describe, expect, test} from "bun:test";
import type {ProviderHealth} from "./chat-api";
import {hubInstance, idleInstance, summary} from "./hub-fixtures";
import {failureText, formatLatency, formatPercent, providerFailing, providerHealthLine, providerHealthTitle, providerHealthTotals} from "./provider-health";

const now = Date.parse("2026-11-14T10:00:00Z");
const health = (overrides: Partial<ProviderHealth> = {}): ProviderHealth => ({
  requests: 10,
  failures: 0,
  client_errors: 0,
  first_byte_p50_ms: 800,
  first_byte_p95_ms: 2400,
  ...overrides,
});
const overloaded = {at: "2026-11-14T09:55:00Z", kind: "status" as const, status: 529, message: "Overloaded"};

describe("provider health", () => {
  test("failing: the latest call failed, or half of the hour's", () => {
    expect(providerFailing(undefined)).toBe(false);
    expect(providerFailing(health())).toBe(false);
    // An earlier failure, answers since.
    expect(providerFailing(health({failures: 1, last_failure: overloaded, last_success_at: "2026-11-14T09:58:00Z"}))).toBe(false);
    expect(providerFailing(health({failures: 1, last_failure: overloaded, last_success_at: "2026-11-14T09:50:00Z"}))).toBe(true);
    expect(providerFailing(health({requests: 2, failures: 2, last_failure: overloaded}))).toBe(true);
    expect(providerFailing(health({failures: 5, last_failure: overloaded, last_success_at: "2026-11-14T09:58:00Z"}))).toBe(true);
    // Refused requests are the agent's, not the provider's failures.
    expect(providerFailing(health({client_errors: 10}))).toBe(false);
  });

  test("figures", () => {
    expect(formatPercent(0)).toBe("0%");
    expect(formatPercent(0.004)).toBe("<1%");
    expect(formatPercent(0.333)).toBe("33%");
    expect(formatLatency(850)).toBe("850 ms");
    expect(formatLatency(1240)).toBe("1.2 s");
    expect(formatLatency(12_400)).toBe("12 s");
    expect(failureText(overloaded)).toBe("529 Overloaded");
    expect(failureText({...overloaded, message: "503 Service Unavailable", status: 503})).toBe("503 Service Unavailable");
    expect(failureText({at: overloaded.at, kind: "timeout", message: "net/http: timeout awaiting response headers"})).toBe("timed out: net/http: timeout awaiting response headers");
    expect(failureText({at: overloaded.at, kind: "network", message: "connection refused"})).toBe("unreachable: connection refused");
  });

  test("tooltip", () => {
    expect(providerHealthTitle(health({failures: 3, client_errors: 1, last_failure: overloaded}), now)).toBe(
      "3 of 10 calls failed in the last hour (30%), 1 refused. First byte in 800 ms (median), 2.4 s (p95). Last error 5 min ago: 529 Overloaded.",
    );
    expect(providerHealthTitle(health({requests: 1, failures: 1, first_byte_p50_ms: 0, last_failure: overloaded}), now)).toBe(
      "1 of 1 call failed in the last hour (100%). Last error 5 min ago: 529 Overloaded.",
    );
  });

  test("totals over the hub's instances", () => {
    const reporting = (id: string, provider_health: NonNullable<ReturnType<typeof summary>["provider_health"]>, connection: "connected" | "unreachable" = "connected") =>
      idleInstance(id, {connection, summary: summary({provider_health})});
    const totals = providerHealthTotals([
      reporting("a", [{provider: "kimi", ...health({requests: 30, failures: 0, first_byte_p50_ms: 500, first_byte_p95_ms: 900, last_success_at: "2026-11-14T09:59:00Z"})}]),
      reporting("b", [
        {provider: "kimi", ...health({requests: 10, failures: 2, client_errors: 1, first_byte_p50_ms: 2000, first_byte_p95_ms: 4000, last_failure: overloaded})},
        {provider: "glm", ...health({requests: 1, failures: 1, first_byte_p50_ms: 0, first_byte_p95_ms: 0, last_failure: {...overloaded, at: "2026-11-14T09:40:00Z"}})},
      ]),
      // The hub doesn't follow it now: its figures may be old.
      reporting("c", [{provider: "kimi", ...health({requests: 100, failures: 100})}], "unreachable"),
      hubInstance("d"),
    ]);
    expect([...totals.keys()].sort()).toEqual(["glm", "kimi"]);
    const kimi = totals.get("kimi")!;
    expect(kimi).toMatchObject({requests: 40, failures: 2, client_errors: 1, first_byte_p95_ms: 4000, last_failure: overloaded, last_success_at: "2026-11-14T09:59:00Z"});
    // The median of the answers: a's 30 at 500 ms outweigh b's 8.
    expect(kimi.first_byte_p50_ms).toBe(500);
    expect(providerFailing(kimi)).toBe(false);
    expect(providerHealthLine(kimi, now)).toBe("1h 40 calls · 5% failed · p50 500 ms · 529 Overloaded 5 min ago");
    const glm = totals.get("glm")!;
    expect(providerFailing(glm)).toBe(true);
    expect(providerHealthLine(glm, now)).toBe("1h 1 call · 100% failed · 529 Overloaded 20 min ago");
    expect(providerHealthTotals([idleInstance("e")]).size).toBe(0);
  });
});
