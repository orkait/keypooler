package contract

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Contract represents a parsed contract.yaml file.
type Contract struct {
	Name      string               `yaml:"name"`
	Runtime   string               `yaml:"runtime"`
	Functions map[string]*Function `yaml:"functions"`
	Path      string               `yaml:"-"` // absolute path to script folder, set after parsing
}

// Function represents a single callable function in a contract.
type Function struct {
	Feature    string            `yaml:"feature"`
	Timeout    string            `yaml:"timeout"`
	Retry      RetryConfig       `yaml:"retry"`
	Scheduling Schedule          `yaml:"scheduling"`
	Input      map[string]string `yaml:"input"`
	Output     map[string]string `yaml:"output"`
}

// RetryConfig defines retry behavior for a function.
type RetryConfig struct {
	Enabled     bool `yaml:"enabled"`
	MaxAttempts int  `yaml:"max_attempts"`
}

// Schedule defines cron scheduling for a function.
type Schedule struct {
	Enabled bool           `yaml:"enabled"`
	Cron    string         `yaml:"cron"`
	Input   map[string]any `yaml:"input"`
}

// TimeoutDuration parses the timeout string (e.g., "60s") into a time.Duration.
func (f *Function) TimeoutDuration() time.Duration {
	d, err := time.ParseDuration(f.Timeout)
	if err != nil {
		return 60 * time.Second // default
	}
	return d
}

// LoadFromDir reads and parses contract.yaml from the given directory.
func LoadFromDir(dir string) (*Contract, error) {
	path := filepath.Join(dir, "contract.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read contract.yaml: %w", err)
	}

	var c Contract
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("failed to parse contract.yaml: %w", err)
	}

	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("invalid contract: %w", err)
	}

	c.Path = dir
	return &c, nil
}

// TriggerFile returns the expected trigger filename based on the runtime.
func (c *Contract) TriggerFile() string {
	switch c.Runtime {
	case "python":
		return "trigger.py"
	case "node", "bun", "deno":
		return "trigger.js"
	default:
		return "trigger.py"
	}
}

// RuntimeBin returns the binary to execute for the given runtime.
func (c *Contract) RuntimeBin() string {
	switch c.Runtime {
	case "python":
		return "python3"
	case "node":
		return "node"
	case "bun":
		return "bun"
	case "deno":
		return "deno"
	default:
		return "python3"
	}
}

// RuntimeArgs returns additional args needed before the script path for the runtime.
func (c *Contract) RuntimeArgs() []string {
	if c.Runtime == "deno" {
		return []string{"run", "--allow-all"}
	}
	return nil
}

func (c *Contract) validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if c.Runtime == "" {
		return fmt.Errorf("runtime is required")
	}
	validRuntimes := map[string]bool{"python": true, "node": true, "bun": true, "deno": true}
	if !validRuntimes[c.Runtime] {
		return fmt.Errorf("invalid runtime %q, must be one of: python, node, bun, deno", c.Runtime)
	}
	if len(c.Functions) == 0 {
		return fmt.Errorf("at least one function is required")
	}
	for name, fn := range c.Functions {
		if fn.Feature == "" {
			return fmt.Errorf("function %q: feature is required", name)
		}
		if fn.Timeout == "" {
			fn.Timeout = "60s"
		}
		if _, err := time.ParseDuration(fn.Timeout); err != nil {
			return fmt.Errorf("function %q: invalid timeout %q", name, fn.Timeout)
		}
		if fn.Retry.Enabled && fn.Retry.MaxAttempts < 1 {
			fn.Retry.MaxAttempts = 3 // default
		}
		if fn.Scheduling.Enabled && fn.Scheduling.Cron == "" {
			return fmt.Errorf("function %q: cron expression required when scheduling is enabled", name)
		}
	}
	return nil
}
