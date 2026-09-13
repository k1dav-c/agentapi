// Package temporalconfig stores portable Temporal/Discord bridge settings.
// Credentials may be stored in the document; named environment variables can
// override them when the worker starts.
package temporalconfig

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	AgentURL          string   `json:"agent_url"`
	TemporalAddress   string   `json:"temporal_address"`
	Namespace         string   `json:"namespace"`
	TaskQueue         string   `json:"task_queue"`
	TemporalTLS       bool     `json:"temporal_tls"`
	ChannelID         string   `json:"channel_id"`
	AllowedUsers      []string `json:"allowed_users" nullable:"false"`
	AgentTokenEnv     string   `json:"agent_token_env"`
	TemporalAPIKeyEnv string   `json:"temporal_api_key_env"`
	BotTokenEnv       string   `json:"bot_token_env"`
	AgentToken        string   `json:"agent_token,omitempty"`
	TemporalAPIKey    string   `json:"temporal_api_key,omitempty"`
	BotToken          string   `json:"bot_token,omitempty"`
}

type Document struct {
	Config Config `json:"config"`
}
type ProfilesDocument struct {
	Profiles map[string]Config `json:"profiles"`
}

func Defaults() Config {
	return Config{AgentURL: "http://localhost:3284", TemporalAddress: "localhost:7233",
		Namespace: defaultNamespace(), AllowedUsers: []string{}, AgentTokenEnv: "AGENTAPI_API_TOKEN",
		TemporalAPIKeyEnv: "TEMPORAL_API_KEY", BotTokenEnv: "DISCORD_BOT_TOKEN"}
}

func defaultNamespace() string {
	name := strings.TrimSpace(os.Getenv("AGENTAPI_SESSION_NAME"))
	if name == "" {
		name = strings.TrimSpace(os.Getenv("CODER_WORKSPACE_NAME"))
	}
	if name == "" {
		return "default"
	}
	var builder strings.Builder
	for _, r := range strings.ToLower(name) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			builder.WriteRune(r)
		} else {
			builder.WriteByte('-')
		}
	}
	value := strings.Trim(builder.String(), "-_.")
	if len(value) > 64 {
		value = strings.TrimRight(value[:64], "-_.")
	}
	if value == "" {
		return "default"
	}
	return value
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var snowflake = regexp.MustCompile(`^[0-9]+$`)

// Validate accepts incomplete Discord credentials/channel selections so profiles
// can be prepared before deployment. Run-time readiness is checked by the worker.
func (c Config) Validate() error {
	u, err := url.Parse(c.AgentURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("agent_url must be an HTTP(S) URL without credentials, query or fragment")
	}
	host, port, err := net.SplitHostPort(c.TemporalAddress)
	number, portErr := strconv.Atoi(port)
	if err != nil || strings.TrimSpace(host) == "" || strings.ContainsAny(host, "/ \t\n") || portErr != nil || number < 1 || number > 65535 {
		return fmt.Errorf("temporal_address must be host:port with a port between 1 and 65535")
	}
	if strings.TrimSpace(c.Namespace) == "" {
		return fmt.Errorf("namespace must not be empty")
	}
	if c.ChannelID != "" && !snowflake.MatchString(c.ChannelID) {
		return fmt.Errorf("channel_id must be a Discord channel ID")
	}
	for _, id := range c.AllowedUsers {
		if !snowflake.MatchString(id) {
			return fmt.Errorf("allowed_users must contain Discord user IDs")
		}
	}
	for _, name := range []string{c.AgentTokenEnv, c.TemporalAPIKeyEnv, c.BotTokenEnv} {
		if name != "" && !envName.MatchString(name) {
			return fmt.Errorf("credential fields must contain environment variable names, not tokens")
		}
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected one JSON document")
	}
	return nil
}

// Parse accepts the same {config: ...} document exported by Session Explorer.
func Parse(data []byte) (Config, error) {
	var document struct {
		Config json.RawMessage `json:"config"`
	}
	if err := decodeStrict(data, &document); err != nil {
		return Config{}, err
	}
	if len(document.Config) == 0 || bytes.Equal(bytes.TrimSpace(document.Config), []byte("null")) {
		return Config{}, fmt.Errorf("config must be an object")
	}
	config := Defaults()
	if err := decodeStrict(document.Config, &config); err != nil {
		return Config{}, err
	}
	if config.AllowedUsers == nil {
		config.AllowedUsers = []string{}
	}
	return config, config.Validate()
}

func ValidProfileName(name string) bool {
	return name != "" && name == strings.TrimSpace(name) && len(name) <= 128 && name != "." && name != ".." && !strings.ContainsAny(name, "/\\\r\n\x00")
}
