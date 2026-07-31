package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

type MCPServerMutationBody struct {
	Name   string `json:"name" minLength:"1" doc:"Unique MCP server name."`
	Config any    `json:"config" doc:"Agent-native MCP server configuration."`
}

type MCPServerConfigBody struct {
	Config any `json:"config" doc:"Agent-native MCP server configuration."`
}

type MCPCheckBody struct {
	Servers MCPServers `json:"servers,omitempty" doc:"Servers to check. When omitted, the saved configuration is checked."`
}

type MCPCheckResult struct {
	Name    string `json:"name"`
	Status  string `json:"status" enum:"ready,unreachable,invalid"`
	Kind    string `json:"kind" enum:"stdio,http,unknown"`
	Detail  string `json:"detail"`
	Latency int64  `json:"latency_ms,omitempty"`
}

type MCPCheckResponse struct {
	Body struct {
		Results []MCPCheckResult `json:"results" nullable:"false"`
	}
}

type MCPProfilesResponse struct {
	Body struct {
		Profiles map[string]MCPServers `json:"profiles" nullable:"false"`
		Path     string                `json:"path"`
	}
}

type MCPProfileBody struct {
	Servers MCPServers `json:"servers" nullable:"false"`
}

func configureMCPChildOperation(summary string) func(*huma.Operation) {
	return func(operation *huma.Operation) {
		operation.Tags = []string{"MCP"}
		operation.Summary = summary
	}
}

func (s *Server) requireMCP() error {
	if s.mcpStore == nil {
		return huma.Error404NotFound("MCP config management is not supported for this agent type")
	}
	return nil
}

func (s *Server) restartQuery(ctx context.Context, restart bool) (bool, error) {
	if !restart {
		return false, nil
	}
	if s.restartAgent == nil {
		return false, huma.Error400BadRequest("agent restart is not supported in this server mode")
	}
	newPID, err := s.restartAgent(ctx)
	if err != nil {
		return false, fmt.Errorf("MCP config was written, but agent restart failed: %w", err)
	}
	s.startJSONLWatcher(newPID)
	return true, nil
}

func (s *Server) mutateMCP(ctx context.Context, restart bool, mutate func(MCPServers) error) (*MCPUpdateResponse, error) {
	if err := s.requireMCP(); err != nil {
		return nil, err
	}
	s.mcpMu.Lock()
	stored, err := s.mcpStore.Read()
	if err != nil {
		s.mcpMu.Unlock()
		return nil, fmt.Errorf("failed to read MCP config: %w", err)
	}
	servers, err := fromConfigServers(stored)
	if err != nil {
		s.mcpMu.Unlock()
		return nil, err
	}
	if err := mutate(servers); err != nil {
		s.mcpMu.Unlock()
		return nil, err
	}
	encoded, err := toConfigServers(servers)
	if err != nil {
		s.mcpMu.Unlock()
		return nil, huma.Error400BadRequest(err.Error())
	}
	if err := s.mcpStore.Replace(encoded); err != nil {
		s.mcpMu.Unlock()
		return nil, fmt.Errorf("failed to write MCP config: %w", err)
	}
	s.mcpMu.Unlock()
	restarted, err := s.restartQuery(ctx, restart)
	if err != nil {
		return nil, err
	}
	response := &MCPUpdateResponse{}
	response.Body.OK = true
	response.Body.Path = s.mcpStore.Path()
	response.Body.Restarted = restarted
	return response, nil
}

func (s *Server) createMCPServer(ctx context.Context, input *struct {
	Restart bool `query:"restart" default:"false"`
	Body    MCPServerMutationBody
},
) (*MCPUpdateResponse, error) {
	return s.mutateMCP(ctx, input.Restart, func(servers MCPServers) error {
		if _, exists := servers[input.Body.Name]; exists {
			return huma.Error409Conflict("an MCP server with this name already exists")
		}
		servers[input.Body.Name] = input.Body.Config
		return nil
	})
}

func (s *Server) patchMCPServer(ctx context.Context, input *struct {
	Name    string `path:"name"`
	Restart bool   `query:"restart" default:"false"`
	Body    MCPServerConfigBody
},
) (*MCPUpdateResponse, error) {
	return s.mutateMCP(ctx, input.Restart, func(servers MCPServers) error {
		if _, exists := servers[input.Name]; !exists {
			return huma.Error404NotFound("MCP server was not found")
		}
		servers[input.Name] = input.Body.Config
		return nil
	})
}

func (s *Server) deleteMCPServer(ctx context.Context, input *struct {
	Name    string `path:"name"`
	Restart bool   `query:"restart" default:"false"`
},
) (*MCPUpdateResponse, error) {
	return s.mutateMCP(ctx, input.Restart, func(servers MCPServers) error {
		if _, exists := servers[input.Name]; !exists {
			return huma.Error404NotFound("MCP server was not found")
		}
		delete(servers, input.Name)
		return nil
	})
}

func (s *Server) checkMCP(ctx context.Context, input *struct{ Body MCPCheckBody }) (*MCPCheckResponse, error) {
	if err := s.requireMCP(); err != nil {
		return nil, err
	}
	servers := input.Body.Servers
	if servers == nil {
		stored, err := s.mcpStore.Read()
		if err != nil {
			return nil, fmt.Errorf("failed to read MCP config: %w", err)
		}
		servers, err = fromConfigServers(stored)
		if err != nil {
			return nil, err
		}
	}
	response := &MCPCheckResponse{}
	response.Body.Results = make([]MCPCheckResult, 0, len(servers))
	for name, config := range servers {
		response.Body.Results = append(response.Body.Results, checkMCPServer(ctx, name, config))
	}
	sort.Slice(response.Body.Results, func(i, j int) bool {
		return response.Body.Results[i].Name < response.Body.Results[j].Name
	})
	return response, nil
}

func checkMCPServer(ctx context.Context, name string, raw any) MCPCheckResult {
	result := MCPCheckResult{Name: name, Status: "invalid", Kind: "unknown"}
	config, ok := raw.(map[string]any)
	if !ok {
		result.Detail = "configuration must be an object"
		return result
	}
	if command, ok := config["command"].(string); ok && command != "" {
		result.Kind = "stdio"
		path, err := exec.LookPath(command)
		if err != nil {
			result.Status = "unreachable"
			result.Detail = "executable was not found in PATH"
			return result
		}
		result.Status = "ready"
		result.Detail = path
		return result
	}
	url, ok := config["url"].(string)
	if !ok || url == "" {
		result.Detail = "configuration requires command or url"
		return result
	}
	result.Kind = "http"
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
	if err != nil {
		result.Detail = err.Error()
		return result
	}
	if headers, ok := config["headers"].(map[string]any); ok {
		for key, value := range headers {
			if text, ok := value.(string); ok {
				request.Header.Set(key, text)
			}
		}
	}
	started := time.Now()
	resp, err := http.DefaultClient.Do(request)
	result.Latency = time.Since(started).Milliseconds()
	if err != nil {
		result.Status = "unreachable"
		result.Detail = err.Error()
		return result
	}
	_ = resp.Body.Close()
	result.Status = "ready"
	result.Detail = resp.Status
	return result
}

func (s *Server) profilesPath() string {
	root := s.cwd
	if root == "" {
		root = filepath.Dir(s.mcpStore.Path())
	}
	return filepath.Join(root, ".agentapi", "mcp-profiles.json")
}

func (s *Server) readMCPProfiles() (map[string]MCPServers, error) {
	profiles := map[string]MCPServers{}
	data, err := os.ReadFile(s.profilesPath())
	if errors.Is(err, os.ErrNotExist) {
		return profiles, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &profiles); err != nil {
		return nil, fmt.Errorf("parse MCP profiles: %w", err)
	}
	return profiles, nil
}

func (s *Server) writeMCPProfiles(profiles map[string]MCPServers) error {
	path := s.profilesPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(profiles, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), "mcp-profiles-*.json")
	if err != nil {
		return err
	}
	tempPath := temporary.Name()
	defer os.Remove(tempPath)
	if _, err := temporary.Write(append(data, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func validProfileName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\`)
}

func (s *Server) getMCPProfiles(_ context.Context, _ *struct{}) (*MCPProfilesResponse, error) {
	if err := s.requireMCP(); err != nil {
		return nil, err
	}
	profiles, err := s.readMCPProfiles()
	if err != nil {
		return nil, fmt.Errorf("failed to read MCP profiles: %w", err)
	}
	response := &MCPProfilesResponse{}
	response.Body.Profiles = profiles
	response.Body.Path = s.profilesPath()
	return response, nil
}

func (s *Server) putMCPProfile(_ context.Context, input *struct {
	Name string `path:"name"`
	Body MCPProfileBody
},
) (*MCPProfilesResponse, error) {
	if err := s.requireMCP(); err != nil {
		return nil, err
	}
	if !validProfileName(input.Name) {
		return nil, huma.Error400BadRequest("invalid MCP profile name")
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	profiles, err := s.readMCPProfiles()
	if err != nil {
		return nil, err
	}
	profiles[input.Name] = input.Body.Servers
	if err := s.writeMCPProfiles(profiles); err != nil {
		return nil, fmt.Errorf("failed to write MCP profiles: %w", err)
	}
	response := &MCPProfilesResponse{}
	response.Body.Profiles = profiles
	response.Body.Path = s.profilesPath()
	return response, nil
}

func (s *Server) deleteMCPProfile(_ context.Context, input *struct {
	Name string `path:"name"`
},
) (*MCPProfilesResponse, error) {
	if err := s.requireMCP(); err != nil {
		return nil, err
	}
	s.mcpMu.Lock()
	defer s.mcpMu.Unlock()
	profiles, err := s.readMCPProfiles()
	if err != nil {
		return nil, err
	}
	if _, exists := profiles[input.Name]; !exists {
		return nil, huma.Error404NotFound("MCP profile was not found")
	}
	delete(profiles, input.Name)
	if err := s.writeMCPProfiles(profiles); err != nil {
		return nil, fmt.Errorf("failed to write MCP profiles: %w", err)
	}
	response := &MCPProfilesResponse{}
	response.Body.Profiles = profiles
	response.Body.Path = s.profilesPath()
	return response, nil
}

func (s *Server) applyMCPProfile(ctx context.Context, input *struct {
	Name    string `path:"name"`
	Restart bool   `query:"restart" default:"false"`
},
) (*MCPUpdateResponse, error) {
	if err := s.requireMCP(); err != nil {
		return nil, err
	}
	profiles, err := s.readMCPProfiles()
	if err != nil {
		return nil, err
	}
	servers, exists := profiles[input.Name]
	if !exists {
		return nil, huma.Error404NotFound("MCP profile was not found")
	}
	return s.mutateMCP(ctx, input.Restart, func(current MCPServers) error {
		for name := range current {
			delete(current, name)
		}
		for name, config := range servers {
			current[name] = config
		}
		return nil
	})
}
