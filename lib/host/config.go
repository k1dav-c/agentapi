package host

import (
	"cmp"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/coder/agentapi/lib/userconfig"
)

// ConfigKey is the section of ~/.agentapi/config.yaml the launcher reads.
// agentapi server leaves it alone.
const ConfigKey = "host"

// Config is the launcher's settings, the host section of the config file.
type Config struct {
	// Port and Bind are where the launcher's API listens.
	Port int    `yaml:"port"`
	Bind string `yaml:"bind"`
	// PublicURL is the URL the hub reaches the launcher at; {port},
	// {hostname} and {ip} are filled in.
	PublicURL string `yaml:"public-url"`
	// TokenFile holds the token the hub calls the launcher with, made when
	// missing.
	TokenFile string `yaml:"token-file"`
	// InstanceTokenFile holds the API token of every job's AgentAPI: the
	// hub's --instance-token, made when missing.
	InstanceTokenFile string `yaml:"instance-token-file"`
	// GitHubTokenFile holds a GitHub token the jobs' git and gh use, until
	// the hub hands them out. Without it they use the machine's own
	// credentials.
	GitHubTokenFile string `yaml:"github-token-file"`
	// Workdir holds the repositories the launcher clones and every job's
	// worktree.
	Workdir string `yaml:"workdir"`
	// Scan are the directories the launcher looks in for repositories it
	// has, two levels deep.
	Scan []string `yaml:"scan"`
	// Ports are the ports of the jobs' AgentAPIs: first-last.
	Ports   string `yaml:"ports"`
	MaxJobs int    `yaml:"max-jobs"`
	// Agent is the agent of a job that names none; Agents the ones this
	// machine runs, by default those whose command is installed.
	Agent  string   `yaml:"agent"`
	Agents []string `yaml:"agents"`
	// AgentCommands is each agent's command line.
	AgentCommands map[string][]string `yaml:"agent-commands"`
	// Clone is whether the launcher clones a repository it doesn't have.
	Clone *bool `yaml:"clone"`
	// AgentAPI is the program the jobs run, by default this one.
	AgentAPI string `yaml:"agentapi"`

	// Path is the config file; the jobs' AgentAPIs read it too (--config),
	// so they report to the launcher's hub. Empty is the default file.
	Path string `yaml:"-"`
	// Dir is ~/.agentapi: the launcher's files.
	Dir string `yaml:"-"`
	// HubURL is the hub's webhook URL, the config file's webhook-url:
	// {ip} in PublicURL is the address on the route to it.
	HubURL string `yaml:"-"`
	// HubSecret signs the launcher's events and token requests: the
	// config file's webhook-secret. HubEvents are its webhook-events.
	HubSecret string   `yaml:"-"`
	HubEvents []string `yaml:"-"`
	// firstPort and lastPort are Ports.
	firstPort, lastPort int
}

// DefaultPort is the launcher API's port.
const DefaultPort = 3291

// defaultAgentCommands start each agent as the Coder templates do.
var defaultAgentCommands = map[string][]string{
	"claude":      {"claude"},
	"codex":       {"codex"},
	"pi":          {"pi", "--approve"},
	"native-demo": {"native-demo"},
}

// detectedAgents are the agents the launcher offers when their command is
// installed; native-demo only when the config names it.
var detectedAgents = []string{"claude", "codex", "pi"}

// LoadConfig reads the host section of the config file at path (by default
// ~/.agentapi/config.yaml) and fills in the defaults.
func LoadConfig(path string) (Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Config{}, fmt.Errorf("find the home directory: %w", err)
	}
	if path == "" {
		if path, err = userconfig.DefaultPath(); err != nil {
			return Config{}, err
		}
	}
	file, err := userconfig.Load(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if section, ok := file[ConfigKey]; ok {
		data, err := yaml.Marshal(section)
		if err != nil {
			return Config{}, err
		}
		if err := yaml.Unmarshal(data, &config); err != nil {
			return Config{}, fmt.Errorf("read %s of %s: %w", ConfigKey, path, err)
		}
	}
	config.Dir = filepath.Dir(path)
	config.Path = path
	config.HubURL, _ = file["webhook-url"].(string)
	config.HubSecret, _ = file["webhook-secret"].(string)
	if events, ok := file["webhook-events"].([]any); ok {
		for _, e := range events {
			if e, ok := e.(string); ok {
				config.HubEvents = append(config.HubEvents, e)
			}
		}
	}
	return config.withDefaults(home)
}

func (c Config) withDefaults(home string) (Config, error) {
	expand := func(p string) string {
		if p == "~" {
			return home
		}
		if rest, ok := strings.CutPrefix(p, "~/"); ok {
			return filepath.Join(home, rest)
		}
		return p
	}
	if c.Dir == "" {
		c.Dir = filepath.Join(home, ".agentapi")
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	c.TokenFile = expand(cmp.Or(c.TokenFile, filepath.Join(c.Dir, "host.token")))
	c.InstanceTokenFile = expand(cmp.Or(c.InstanceTokenFile, filepath.Join(c.Dir, "instance.token")))
	c.GitHubTokenFile = expand(cmp.Or(c.GitHubTokenFile, filepath.Join(c.Dir, "github.token")))
	c.Workdir = expand(cmp.Or(c.Workdir, filepath.Join(home, "work")))
	if len(c.Scan) == 0 {
		c.Scan = []string{c.Workdir}
	}
	for i, dir := range c.Scan {
		c.Scan[i] = expand(dir)
	}
	if !slices.Contains(c.Scan, c.Workdir) {
		c.Scan = append([]string{c.Workdir}, c.Scan...)
	}
	first, last, err := parsePorts(cmp.Or(c.Ports, "4100-4199"))
	if err != nil {
		return Config{}, err
	}
	c.firstPort, c.lastPort = first, last
	if c.MaxJobs <= 0 {
		c.MaxJobs = 4
	}
	if err := c.setAgents(); err != nil {
		return Config{}, err
	}
	if c.AgentAPI == "" {
		if c.AgentAPI, err = Executable(); err != nil {
			return Config{}, err
		}
	}
	c.AgentAPI = expand(c.AgentAPI)
	return c, nil
}

// setAgents completes the agents: their commands (the defaults, with
// agent-commands over them), the agents to run (those whose command is
// installed, when none are listed) and the default agent (the first).
func (c *Config) setAgents() error {
	commands := map[string][]string{}
	for agent, command := range defaultAgentCommands {
		commands[agent] = command
	}
	for agent, command := range c.AgentCommands {
		if len(command) == 0 {
			return fmt.Errorf("agent-commands.%s is empty", agent)
		}
		commands[agent] = command
	}
	c.AgentCommands = commands
	if len(c.Agents) == 0 {
		for _, agent := range detectedAgents {
			if _, err := exec.LookPath(commands[agent][0]); err == nil {
				c.Agents = append(c.Agents, agent)
			}
		}
	}
	for _, agent := range c.Agents {
		if _, ok := commands[agent]; !ok {
			return fmt.Errorf("agent %q has no command: add it to agent-commands", agent)
		}
	}
	if c.Agent == "" && len(c.Agents) > 0 {
		c.Agent = c.Agents[0]
	}
	if c.Agent != "" && !slices.Contains(c.Agents, c.Agent) {
		return fmt.Errorf("agent %q isn't one of this machine's agents %v", c.Agent, c.Agents)
	}
	return nil
}

// CloneMissing is whether the launcher clones a repository it doesn't have.
func (c Config) CloneMissing() bool { return c.Clone == nil || *c.Clone }

// JobsDir holds a directory for each job.
func (c Config) JobsDir() string { return filepath.Join(c.Dir, "jobs") }

// StateFile is the launcher's list of jobs.
func (c Config) StateFile() string { return filepath.Join(c.Dir, "jobs.json") }

// SocketPath is where the jobs' git and gh ask the launcher for a token.
func (c Config) SocketPath() string { return filepath.Join(c.Dir, "host.sock") }

func parsePorts(s string) (first, last int, err error) {
	a, b, ok := strings.Cut(s, "-")
	if !ok {
		b = a
	}
	first, err1 := strconv.Atoi(strings.TrimSpace(a))
	last, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || first < 1 || last > 65535 || first > last {
		return 0, 0, fmt.Errorf("ports %q: want first-last, such as 4100-4199", s)
	}
	return first, last, nil
}

// Executable is the path of this program, as PATH names it when it does
// (a symlink such as /usr/local/bin/agentapi), so a later update of the
// file it points to applies.
func Executable() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("find this program: %w", err)
	}
	if onPath, err := exec.LookPath("agentapi"); err == nil {
		if a, errA := os.Stat(onPath); errA == nil {
			if b, errB := os.Stat(exe); errB == nil && os.SameFile(a, b) {
				if abs, err := filepath.Abs(onPath); err == nil {
					return abs, nil
				}
			}
		}
	}
	return exe, nil
}
