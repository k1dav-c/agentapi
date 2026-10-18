package mcpconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/coder/agentapi/lib/agenthome"
)

type codexStore struct {
	path string
}

func newCodexStore() (*codexStore, error) {
	root, err := agenthome.CodexHome()
	if err != nil {
		return nil, err
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
	mcpHeaderRE = regexp.MustCompile(`^\s*\[{1,2}\s*(?:"mcp_servers"|mcp_servers)\s*[\].]`)
	anyHeaderRE = regexp.MustCompile(`^\s*\[`)
	// rootMCPAssignRE matches mcp_servers set in the root table: as a whole
	// (mcp_servers = {...}) or by dotted keys (mcp_servers.docs.command = ...).
	rootMCPAssignRE = regexp.MustCompile(`^\s*(?:"mcp_servers"|mcp_servers)\s*[=.]`)
	managedMarkerRE = regexp.MustCompile(`^\s*# (?:>>>|<<<) managed by agentapi`)
)

// openBrackets returns how many more brackets and braces line opens than
// it closes, outside strings and comments: a value that goes on on the
// next lines (a multi-line array) leaves some open.
func openBrackets(line string) int {
	depth := 0
	var quote rune
	escaped := false
	for _, r := range line {
		switch {
		case quote != 0:
			switch {
			case escaped:
				escaped = false
			case r == '\\' && quote == '"':
				escaped = true
			case r == quote:
				quote = 0
			}
		case r == '"' || r == '\'':
			quote = r
		case r == '#':
			return depth
		case r == '[' || r == '{':
			depth++
		case r == ']' || r == '}':
			depth--
		}
	}
	return depth
}

func (s *codexStore) Replace(servers Servers) error {
	data, err := os.ReadFile(s.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read %s: %w", s.path, err)
	}

	var kept []string
	inMCPSection := false
	inRootTable := true
	// skipping is how many brackets the root mcp_servers value being
	// dropped still has open.
	skipping := 0
	for _, line := range strings.Split(string(data), "\n") {
		if skipping > 0 {
			skipping += openBrackets(line)
			continue
		}
		if managedMarkerRE.MatchString(line) {
			continue
		}
		if anyHeaderRE.MatchString(line) {
			inRootTable = false
			inMCPSection = mcpHeaderRE.MatchString(line)
		} else if inRootTable && rootMCPAssignRE.MatchString(line) {
			skipping = max(openBrackets(line), 0)
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
	// Rewriting by lines can't follow every way TOML can spell a table;
	// writing a file Codex can't read would break it, so check first.
	if err := checkCodexServers(content, servers); err != nil {
		return fmt.Errorf("can't rewrite the MCP servers in %s (edit it by hand): %w", s.path, err)
	}
	return writeFile(s.path, []byte(content))
}

// checkCodexServers checks that content parses and that its mcp_servers
// are servers.
func checkCodexServers(content string, servers Servers) error {
	var document struct {
		MCPServers map[string]any `toml:"mcp_servers"`
	}
	if err := toml.Unmarshal([]byte(content), &document); err != nil {
		return err
	}
	got, err := json.Marshal(document.MCPServers)
	if err != nil {
		return err
	}
	want := map[string]any{}
	for name, raw := range servers {
		var config any
		if err := json.Unmarshal(raw, &config); err != nil {
			return err
		}
		want[name] = config
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		return err
	}
	if len(want) == 0 && len(document.MCPServers) == 0 {
		return nil
	}
	if !jsonEqual(got, wantJSON) {
		return errors.New("the servers written don't read back the same")
	}
	return nil
}

// jsonEqual reports whether two JSON documents hold the same values.
func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
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
