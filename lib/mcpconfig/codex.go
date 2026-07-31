package mcpconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

type codexStore struct {
	path string
}

func newCodexStore() (*codexStore, error) {
	root := os.Getenv("CODEX_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir: %w", err)
		}
		root = filepath.Join(home, ".codex")
	}
	return &codexStore{path: filepath.Join(root, "config.toml")}, nil
}

func (s *codexStore) Path() string { return s.path }

func (s *codexStore) Read() (Servers, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Servers{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", s.path, err)
	}
	var document struct {
		MCPServers map[string]any `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse %s: %w", s.path, err)
	}
	servers := Servers{}
	for name, config := range document.MCPServers {
		raw, err := json.Marshal(config)
		if err != nil {
			return nil, fmt.Errorf("encode mcp server %q: %w", name, err)
		}
		servers[name] = raw
	}
	return servers, nil
}

var (
	mcpHeaderRE     = regexp.MustCompile(`^\s*\[{1,2}\s*(?:"mcp_servers"|mcp_servers)\s*[\].]`)
	anyHeaderRE     = regexp.MustCompile(`^\s*\[`)
	rootMCPAssignRE = regexp.MustCompile(`^\s*(?:"mcp_servers"|mcp_servers)\s*=`)
	managedMarkerRE = regexp.MustCompile(`^\s*# (?:>>>|<<<) managed by agentapi`)
)

func (s *codexStore) Replace(servers Servers) error {
	data, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", s.path, err)
	}

	var kept []string
	inMCPSection := false
	inRootTable := true
	for _, line := range strings.Split(string(data), "\n") {
		if managedMarkerRE.MatchString(line) {
			continue
		}
		if anyHeaderRE.MatchString(line) {
			inRootTable = false
			inMCPSection = mcpHeaderRE.MatchString(line)
		} else if inRootTable && rootMCPAssignRE.MatchString(line) {
			continue
		}
		if !inMCPSection {
			kept = append(kept, line)
		}
	}
	content := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	encoded, err := encodeCodexServers(servers)
	if err != nil {
		return err
	}
	if encoded != "" {
		if content != "" {
			content += "\n"
		}
		content += "\n# >>> managed by agentapi (PUT /mcp) >>>\n" +
			encoded + "# <<< managed by agentapi <<<"
	}
	content += "\n"
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return fmt.Errorf("create %s: %w", filepath.Dir(s.path), err)
	}
	if err := os.WriteFile(s.path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", s.path, err)
	}
	return nil
}

func encodeCodexServers(servers Servers) (string, error) {
	if len(servers) == 0 {
		return "", nil
	}
	decoded := map[string]any{}
	for name, raw := range servers {
		var config any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&config); err != nil {
			return "", fmt.Errorf("parse mcp server %q: %w", name, err)
		}
		decoded[name] = normalizeJSONNumbers(config)
	}
	var output bytes.Buffer
	if err := toml.NewEncoder(&output).Encode(map[string]any{"mcp_servers": decoded}); err != nil {
		return "", fmt.Errorf("encode mcp_servers as TOML: %w", err)
	}
	return output.String(), nil
}

func normalizeJSONNumbers(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizeJSONNumbers(item)
		}
	case []any:
		for index, item := range typed {
			typed[index] = normalizeJSONNumbers(item)
		}
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer
		}
		if decimal, err := typed.Float64(); err == nil {
			return decimal
		}
		return typed.String()
	}
	return value
}
