package httpapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/coder/agentapi/lib/mcpconfig"
	"github.com/danielgtaylor/huma/v2"
)

type MCPServers map[string]any

type MCPGetResponse struct {
	Body struct {
		Servers MCPServers `json:"servers" nullable:"false" doc:"Agent-native MCP server objects keyed by unique server name. Values are returned verbatim and may contain commands, arguments, environment variables, URLs, or headers."`
		Path    string     `json:"path" doc:"Absolute path of the managed config file: <working-directory>/.mcp.json for Claude or $CODEX_HOME/config.toml (default ~/.codex/config.toml) for Codex."`
	}
}

type MCPUpdateRequestBody struct {
	Servers MCPServers `json:"servers" nullable:"false" doc:"Complete agent-native MCP server map. This is replacement semantics: existing MCP servers omitted from this map are removed. An empty object removes every MCP server. Unrelated settings in the config file are preserved."`
}

type MCPUpdateResponse struct {
	Body struct {
		OK        bool   `json:"ok" doc:"True when the complete MCP server map was written successfully."`
		Path      string `json:"path" doc:"Absolute path of the config file that was written."`
		Restarted bool   `json:"restarted" doc:"True when restart=true was requested and the PTY agent process was replaced successfully."`
	}
}

func configureMCPGetOperation(operation *huma.Operation) {
	operation.Tags = []string{"MCP"}
	operation.Summary = "Get MCP server configuration"
	operation.Description = `Returns the complete MCP server configuration used by the running agent.

Supported agents and files:

- **Claude:** project-scoped ` + "`.mcp.json`" + ` in the agent working directory (` + "`mcpServers`" + ` object).
- **Codex:** ` + "`$CODEX_HOME/config.toml`" + `, falling back to ` + "`~/.codex/config.toml`" + ` (` + "`[mcp_servers.*]`" + ` tables).

Server values are agent-native and passed through without transport-specific validation. The response can contain environment values or HTTP headers, so treat it as sensitive. Other agent types return ` + "`404`" + `.`
	operation.Errors = []int{404, 500}
}

func configureMCPUpdateOperation(operation *huma.Operation) {
	operation.Tags = []string{"MCP"}
	operation.Summary = "Replace MCP server configuration"
	operation.Description = `Replaces the agent's **complete** MCP server map while preserving unrelated config-file content.

Important behavior:

- Servers omitted from ` + "`servers`" + ` are removed; send ` + "`{\"servers\": {}}`" + ` to clear all MCP servers.
- Values use the target agent's native format and are not validated by AgentAPI.
- Without ` + "`restart=true`" + `, the new configuration is loaded by the next agent session.
- With ` + "`restart=true`" + `, AgentAPI replaces the PTY child process immediately. AgentAPI and its HTTP/SSE endpoints stay online, but the agent's conversation context is reset.
- Restart is unavailable in ACP, schema-only, and other server modes without a process supervisor; those requests return ` + "`400`" + `.
- A missing or structurally invalid request body returns ` + "`422`" + `.
- Claude and Codex are supported; other agent types return ` + "`404`" + `.`
	operation.Errors = []int{400, 404, 500}
}

func addMCPExamples(operation *huma.Operation, includeRequest bool) {
	claudeServers := MCPServers{
		"fetch": map[string]any{
			"command": "uvx",
			"args":    []string{"mcp-server-fetch"},
			"env":     map[string]string{"LOG_LEVEL": "info"},
		},
		"remote-api": map[string]any{
			"type":    "http",
			"url":     "https://mcp.example.com/mcp",
			"headers": map[string]string{"Authorization": "Bearer ${MCP_TOKEN}"},
		},
	}
	codexServers := MCPServers{
		"fetch": map[string]any{
			"command": "uvx",
			"args":    []string{"mcp-server-fetch"},
		},
		"remote-api": map[string]any{
			"url":                  "https://mcp.example.com/mcp",
			"bearer_token_env_var": "MCP_TOKEN",
			"startup_timeout_sec":  20,
		},
	}
	if includeRequest && operation.RequestBody != nil {
		if media := operation.RequestBody.Content["application/json"]; media != nil {
			media.Examples = map[string]*huma.Example{
				"claude": {
					Summary:     "Claude stdio and remote HTTP servers",
					Description: "Writes these entries beneath mcpServers in the project .mcp.json file.",
					Value:       MCPUpdateRequestBody{Servers: claudeServers},
				},
				"codex": {
					Summary:     "Codex stdio and remote HTTP servers",
					Description: "Writes these entries as mcp_servers tables in Codex config.toml.",
					Value:       MCPUpdateRequestBody{Servers: codexServers},
				},
				"clear-all": {
					Summary:     "Remove all MCP servers",
					Description: "Unrelated Claude or Codex settings remain unchanged.",
					Value:       MCPUpdateRequestBody{Servers: MCPServers{}},
				},
			}
		}
	}
	if response := operation.Responses["200"]; response != nil {
		if media := response.Content["application/json"]; media != nil {
			if includeRequest {
				media.Examples = map[string]*huma.Example{
					"saved": {
						Summary: "Saved for the next session",
						Value: map[string]any{
							"ok": true, "path": "/home/agent/.codex/config.toml", "restarted": false,
						},
					},
					"restarted": {
						Summary: "Saved and applied immediately",
						Value: map[string]any{
							"ok": true, "path": "/workspace/.mcp.json", "restarted": true,
						},
					},
				}
			} else {
				media.Examples = map[string]*huma.Example{
					"claude": {
						Summary: "Claude project MCP configuration",
						Value: map[string]any{
							"servers": claudeServers,
							"path":    "/workspace/.mcp.json",
						},
					},
					"codex": {
						Summary: "Codex user MCP configuration",
						Value: map[string]any{
							"servers": codexServers,
							"path":    "/home/agent/.codex/config.toml",
						},
					},
				}
			}
		}
	}
	errorExamples := map[string]struct {
		summary string
		detail  string
		status  int
	}{
		"400": {
			summary: "Restart unavailable",
			detail:  "agent restart is not supported in this server mode",
			status:  400,
		},
		"404": {
			summary: "Unsupported agent",
			detail:  "MCP config management is not supported for this agent type",
			status:  404,
		},
		"500": {
			summary: "Config file failure",
			detail:  "failed to write MCP config",
			status:  500,
		},
	}
	for status, example := range errorExamples {
		response := operation.Responses[status]
		if response == nil {
			continue
		}
		media := response.Content["application/problem+json"]
		if media == nil {
			continue
		}
		media.Examples = map[string]*huma.Example{
			"error": {
				Summary: example.summary,
				Value: map[string]any{
					"title":  response.Description,
					"status": example.status,
					"detail": example.detail,
				},
			},
		}
	}
}

func toConfigServers(servers MCPServers) (mcpconfig.Servers, error) {
	output := mcpconfig.Servers{}
	for name, config := range servers {
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("encode MCP server %q: %w", name, err)
		}
		output[name] = raw
	}
	return output, nil
}

func fromConfigServers(servers mcpconfig.Servers) (MCPServers, error) {
	output := MCPServers{}
	for name, raw := range servers {
		var config any
		if err := json.Unmarshal(raw, &config); err != nil {
			return nil, fmt.Errorf("decode MCP server %q: %w", name, err)
		}
		output[name] = config
	}
	return output, nil
}

func (s *Server) getMCP(
	_ context.Context,
	_ *struct{},
) (*MCPGetResponse, error) {
	if s.mcpStore == nil {
		return nil, huma.Error404NotFound(
			"MCP config management is not supported for this agent type",
		)
	}
	servers, err := s.mcpStore.Read()
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP config: %w", err)
	}
	apiServers, err := fromConfigServers(servers)
	if err != nil {
		return nil, err
	}
	response := &MCPGetResponse{}
	response.Body.Servers = apiServers
	response.Body.Path = s.mcpStore.Path()
	return response, nil
}

func (s *Server) updateMCP(
	ctx context.Context,
	input *struct {
		Restart bool `query:"restart" default:"false" doc:"Restart the agent after writing the config so changes apply immediately. The conversation context is reset; AgentAPI stays online. Supported only for supervised PTY sessions."`
		Body    MCPUpdateRequestBody
	},
) (*MCPUpdateResponse, error) {
	if s.mcpStore == nil {
		return nil, huma.Error404NotFound(
			"MCP config management is not supported for this agent type",
		)
	}
	if input.Restart && s.restartAgent == nil {
		return nil, huma.Error400BadRequest(
			"agent restart is not supported in this server mode",
		)
	}
	servers, err := toConfigServers(input.Body.Servers)
	if err != nil {
		return nil, huma.Error400BadRequest(err.Error())
	}
	if err := s.mcpStore.Replace(servers); err != nil {
		return nil, fmt.Errorf("failed to write MCP config: %w", err)
	}
	response := &MCPUpdateResponse{}
	response.Body.OK = true
	response.Body.Path = s.mcpStore.Path()
	restarted, err := s.restartQuery(ctx, input.Restart)
	if err != nil {
		return nil, err
	}
	response.Body.Restarted = restarted
	return response, nil
}
