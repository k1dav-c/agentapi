import {describe, expect, test} from "bun:test";
import {formatUSDShort} from "./format";
import {providerAmount, providerUsageLabel, usesOtherProviders} from "./provider-usage";

describe("provider usage", () => {
  test("the agent's own provider says so; another goes by its name, else its ID", () => {
    const names = new Map([["gateway", "Team gateway"]]);
    expect(providerUsageLabel("default", names)).toBe("Own provider");
    expect(providerUsageLabel("gateway", names)).toBe("Team gateway");
    expect(providerUsageLabel("vllm", names)).toBe("vllm");
    expect(providerUsageLabel("gateway")).toBe("gateway");
  });

  test("only calls to another provider make the list worth showing", () => {
    expect(usesOtherProviders(undefined)).toBe(false);
    expect(usesOtherProviders([])).toBe(false);
    expect(usesOtherProviders([{provider: "default"}])).toBe(false);
    expect(usesOtherProviders([{provider: "default"}, {provider: "gateway"}])).toBe(true);
  });

  test("a cost without a known price says so rather than $0.00", () => {
    expect(providerAmount(1.234, true)).toBe("$1.23");
    expect(providerAmount(0, true)).toBe("$0.00");
    expect(providerAmount(0, false)).toBe("no price");
    expect(providerAmount(2.5, false)).toBe("$2.50+");
    expect(providerAmount(120.4, true, formatUSDShort)).toBe("$120");
  });
});
