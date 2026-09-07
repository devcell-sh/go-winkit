package config

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type CommandEntry struct {
	Cmd            string        `yaml:"cmd"`
	Timeout        duration      `yaml:"timeout"`
	Retries        int           `yaml:"retries"`
	ValidExitCodes []int         `yaml:"valid-exit-codes"`
	Reboot         bool          `yaml:"reboot"`
}

type duration time.Duration

func (d *duration) UnmarshalYAML(value *yaml.Node) error {
	var s string
	if err := value.Decode(&s); err != nil {
		return err
	}
	parsed, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	*d = duration(parsed)
	return nil
}

func (d duration) Duration() time.Duration {
	return time.Duration(d)
}

type FileEntry struct {
	Src  string `yaml:"src"`
	Dest string `yaml:"dest"`
}

type WSLConfig struct {
	Image string `yaml:"image"`
}

type Config struct {
	From     string                       `yaml:"from"`
	PE       bool                         `yaml:"pe"`
	WSL      *WSLConfig                   `yaml:"wsl"`
	Features []string                     `yaml:"features"`
	Files    []FileEntry                  `yaml:"files"`
	Commands commandsField                `yaml:"-"`
}

type commandsField struct {
	Phases map[string][]CommandEntry
}

func (c *Config) UnmarshalYAML(value *yaml.Node) error {
	type plain struct {
		From     string      `yaml:"from"`
		PE       bool        `yaml:"pe"`
		WSL      *WSLConfig  `yaml:"wsl"`
		Features []string    `yaml:"features"`
		Files    []FileEntry `yaml:"files"`
	}

	var p plain
	if err := value.Decode(&p); err != nil {
		return err
	}
	c.From = p.From
	c.PE = p.PE
	c.WSL = p.WSL
	c.Features = p.Features
	c.Files = p.Files
	c.Commands.Phases = make(map[string][]CommandEntry)

	cmdNode := findKey(value, "commands")
	if cmdNode == nil {
		return nil
	}

	if cmdNode.Kind == yaml.SequenceNode {
		entries, err := parseCommandList(cmdNode)
		if err != nil {
			return fmt.Errorf("commands: %w", err)
		}
		c.Commands.Phases["boot"] = entries
		return nil
	}

	if cmdNode.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(cmdNode.Content); i += 2 {
			phase := cmdNode.Content[i].Value
			entries, err := parseCommandList(cmdNode.Content[i+1])
			if err != nil {
				return fmt.Errorf("commands.%s: %w", phase, err)
			}
			c.Commands.Phases[phase] = entries
		}
		return nil
	}

	return fmt.Errorf("commands: expected list or map, got %v", cmdNode.Kind)
}

func findKey(mapping *yaml.Node, key string) *yaml.Node {
	if mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

func parseCommandList(node *yaml.Node) ([]CommandEntry, error) {
	var result []CommandEntry
	for _, item := range node.Content {
		switch item.Kind {
		case yaml.ScalarNode:
			var s string
			if err := item.Decode(&s); err != nil {
				return nil, err
			}
			result = append(result, CommandEntry{Cmd: s})
		case yaml.MappingNode:
			var entry CommandEntry
			if err := item.Decode(&entry); err != nil {
				return nil, err
			}
			result = append(result, entry)
		default:
			return nil, fmt.Errorf("unexpected node type %v in command list", item.Kind)
		}
	}
	return result, nil
}

var defaultNames = []string{"winkit.yaml", "winkit.yml"}

func Discover(dir string) (string, error) {
	for _, name := range defaultNames {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("no winkit.yaml or winkit.yml found in %s", dir)
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	return Parse(data)
}

func Parse(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.PE && c.WSL != nil {
		return fmt.Errorf("pe and wsl are mutually exclusive: WinPE cannot run WSL")
	}
	if c.PE {
		for phase := range c.Commands.Phases {
			if phase != "boot" {
				return fmt.Errorf("pe mode only supports boot phase commands, got %s", phase)
			}
		}
	}
	if c.WSL == nil {
		if _, ok := c.Commands.Phases["wsl"]; ok {
			return fmt.Errorf("wsl phase commands require wsl to be enabled")
		}
	}
	validPhases := map[string]bool{"specialize": true, "oobe": true, "boot": true, "wsl": true}
	for phase := range c.Commands.Phases {
		if !validPhases[phase] {
			return fmt.Errorf("unknown command phase %q (want specialize, oobe, boot, or wsl)", phase)
		}
	}
	return nil
}

func (c *Config) LoadDirectoryHooks(dir string) error {
	hooksDir := filepath.Join(dir, "hooks")
	info, err := os.Stat(hooksDir)
	if err != nil || !info.IsDir() {
		return nil
	}

	phases := []string{"specialize", "oobe", "boot", "wsl"}
	for _, phase := range phases {
		phaseDir := filepath.Join(hooksDir, phase)
		entries, err := os.ReadDir(phaseDir)
		if err != nil {
			continue
		}

		var scripts []string
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			scripts = append(scripts, e.Name())
		}
		sort.Strings(scripts)

		for _, name := range scripts {
			fullPath := filepath.Join(phaseDir, name)
			data, err := os.ReadFile(fullPath)
			if err != nil {
				return fmt.Errorf("reading hook %s: %w", fullPath, err)
			}
			cmd := strings.TrimSpace(string(data))
			if cmd == "" {
				continue
			}
			if c.Commands.Phases == nil {
				c.Commands.Phases = make(map[string][]CommandEntry)
			}
			c.Commands.Phases[phase] = append(c.Commands.Phases[phase], CommandEntry{Cmd: cmd})
		}
	}
	return nil
}
