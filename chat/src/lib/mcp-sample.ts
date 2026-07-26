export const mcpServerSample: Record<string, unknown> = {
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
};

export function editableMCPServers(
  servers: Record<string, unknown>,
): {servers: Record<string, unknown>; isSample: boolean} {
  if (Object.keys(servers).length > 0) {
    return {servers, isSample: false};
  }

  return {servers: mcpServerSample, isSample: true};
}
