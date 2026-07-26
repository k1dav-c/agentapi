package httpapi

import (
	"context"
	"encoding/json"

	"github.com/coder/agentapi/lib/mcpconfig"
	"github.com/danielgtaylor/huma/v2"
	"golang.org/x/xerrors"
)

type MCPServers map[string]any

type MCPGetResponse struct {
	Body struct {
		Servers MCPServers `json:"servers" nullable:"false" doc:"Currently configured MCP servers, keyed by name"`
		Path    string     `json:"path" doc:"Config file the servers were read from"`
	}
}

type MCPUpdateRequestBody struct {
	Servers MCPServers `json:"servers" nullable:"false" doc:"Complete MCP server set to install. Existing MCP servers not listed here are removed."`
}

type MCPUpdateResponse struct {
	Body struct {
		OK        bool   `json:"ok" doc:"Whether the config was written"`
		Path      string `json:"path" doc:"Config file that was written"`
		Restarted bool   `json:"restarted" doc:"Whether the agent process was restarted"`
	}
}

func toConfigServers(servers MCPServers) (mcpconfig.Servers, error) {
	output := mcpconfig.Servers{}
	for name, config := range servers {
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, xerrors.Errorf("encode MCP server %q: %w", name, err)
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
			return nil, xerrors.Errorf("decode MCP server %q: %w", name, err)
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
		return nil, xerrors.Errorf("failed to read MCP config: %w", err)
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
		Restart bool `query:"restart" doc:"Restart the agent after writing the config so changes apply immediately. The conversation context is reset."`
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
		return nil, xerrors.Errorf("failed to write MCP config: %w", err)
	}
	response := &MCPUpdateResponse{}
	response.Body.OK = true
	response.Body.Path = s.mcpStore.Path()
	if input.Restart {
		if err := s.restartAgent(ctx); err != nil {
			return nil, xerrors.Errorf(
				"MCP config written to %s, but agent restart failed: %w",
				s.mcpStore.Path(),
				err,
			)
		}
		response.Body.Restarted = true
	}
	return response, nil
}
