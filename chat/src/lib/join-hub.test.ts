import {describe, expect, test} from "bun:test";
import {HUB_PLACEHOLDER, hubAddress, joinCommands, joinedHub, loopback} from "./join-hub";

describe("hubAddress", () => {
  test("a host, a host and port, or a URL of the hub", () => {
    expect(hubAddress("")).toBe("");
    expect(hubAddress(" hub.vpn:3290 ")).toBe("http://hub.vpn:3290");
    expect(hubAddress("https://hub.example.com/")).toBe("https://hub.example.com");
    expect(hubAddress("http://hub.vpn:3290/hooks")).toBe("http://hub.vpn:3290");
    expect(hubAddress("https://coder.example.com/apps/hub/chat/hub/")).toBe("https://coder.example.com/apps/hub");
  });
});

describe("joinedHub", () => {
  test("a webhook that agentapi hub join set", () => {
    expect(joinedHub("http://127.0.0.1:3290/hooks")).toBe("http://127.0.0.1:3290");
    expect(joinedHub("https://hooks.example.com/agentapi")).toBeUndefined();
    expect(joinedHub("")).toBeUndefined();
  });
});

describe("loopback", () => {
  test("only on the hub's own machine", () => {
    expect(loopback("http://127.0.0.1:3290")).toBe(true);
    expect(loopback("http://localhost:3290")).toBe(true);
    expect(loopback("http://hub.vpn:3290")).toBe(false);
  });
});

describe("joinCommands", () => {
  test("fill in the hub, or a placeholder for it", () => {
    const filled = joinCommands("http://hub.vpn:3290");
    expect(filled.join).toContain("agentapi hub join http://hub.vpn:3290/hooks --secret \"$HUB_SECRET\" --public-url 'http://{hostname}:{port}'");
    expect(filled.open).toBe("http://hub.vpn:3290/chat/hub/");
    expect(filled.serve).toContain('agentapi hub serve --secret "$HUB_SECRET" --token "$HUB_TOKEN"');
    expect(joinCommands("").join).toContain(`${HUB_PLACEHOLDER}/hooks`);
  });
});
