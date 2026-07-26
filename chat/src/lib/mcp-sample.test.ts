import {describe, expect, test} from "bun:test";
import {editableMCPServers, mcpServerSample} from "./mcp-sample";

describe("editableMCPServers", () => {
  test("provides an editable sample for an empty configuration", () => {
    expect(editableMCPServers({})).toEqual({
      servers: mcpServerSample,
      isSample: true,
    });
    expect(mcpServerSample).toEqual({
      filesystem: {
        command: "npx",
        args: [
          "-y",
          "@modelcontextprotocol/server-filesystem",
          "/path/to/allowed/directory",
        ],
      },
      "remote-streamable": {
        type: "http",
        url: "https://mcp.example.com/mcp",
      },
    });
  });

  test("keeps an existing configuration unchanged", () => {
    const servers = {fetch: {command: "uvx", args: ["mcp-server-fetch"]}};

    expect(editableMCPServers(servers)).toEqual({
      servers,
      isSample: false,
    });
  });
});
