package contract

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Contract represents a JSON contract manifest for compatibility with import flows.
type Contract struct {
	Name      string               `json:"name"`
	Runtime   string               `json:"runtime"`
	Functions map[string]*Function `json:"functions"`
	Path      string               `json:"-"` // absolute path to script folder, set after parsing
}

// Function represents the execution policy for a single callable function.
type Function struct {
	Feature    string            `json:"feature"`
	Timeout    string            `json:"timeout"`
	Retry      RetryConfig       `json:"retry"`
	Scheduling Schedule          `json:"scheduling"`
	Input      map[string]string `json:"input"`
	Output     map[string]string `json:"output"`
}

// RetryConfig defines retry behavior for a function.
type RetryConfig struct {
	Enabled     bool `json:"enabled"`
	MaxAttempts int  `json:"max_attempts"`
}

// Schedule defines cron scheduling for a function.
type Schedule struct {
	Enabled bool           `json:"enabled"`
	Cron    string         `json:"cron"`
	Input   map[string]any `json:"input"`
}

var validRuntimes = map[string]bool{
	"python":     true,
	"node":       true,
	"bun":        true,
	"deno":       true,
	"typescript": true,
	"go":         true,
}

// TimeoutDuration parses the timeout string (e.g., "60s") into a time.Duration.
func (f *Function) TimeoutDuration() time.Duration {
	d, err := time.ParseDuration(f.Timeout)
	if err != nil {
		return 60 * time.Second // default
	}
	return d
}

// ParseFunction decodes a JSON function contract and normalizes defaults.
func ParseFunction(data []byte) (*Function, error) {
	var fn Function
	if err := json.Unmarshal(data, &fn); err != nil {
		return nil, fmt.Errorf("failed to parse contract JSON: %w", err)
	}
	if err := fn.Validate(); err != nil {
		return nil, err
	}
	return &fn, nil
}

// LoadFromDir reads and parses contract.json from the given directory.
func LoadFromDir(dir string) (*Contract, error) {
	path := filepath.Join(dir, "contract.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read contract.json: %w", err)
	}

	var c Contract
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("failed to parse contract.json: %w", err)
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
	case "typescript":
		return "trigger.ts"
	case "go":
		return "trigger.go"
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
	case "bun", "typescript":
		return "bun"
	case "deno":
		return "deno"
	case "go":
		return "go"
	default:
		return "python3"
	}
}

// RuntimeArgs returns additional args needed before the script path for the runtime.
func (c *Contract) RuntimeArgs() []string {
	switch c.Runtime {
	case "deno":
		return []string{"run", "--allow-all"}
	case "go":
		return []string{"run"}
	default:
		return nil
	}
}

// ValidateRuntime checks if a runtime is supported.
func ValidateRuntime(runtime string) error {
	if !validRuntimes[runtime] {
		return fmt.Errorf("invalid runtime %q, must be one of: python, node, bun, deno, typescript, go", runtime)
	}
	return nil
}

// Validate normalizes defaults and checks required fields.
func (f *Function) Validate() error {
	if f.Timeout == "" {
		f.Timeout = "60s"
	}
	if _, err := time.ParseDuration(f.Timeout); err != nil {
		return fmt.Errorf("invalid timeout %q", f.Timeout)
	}
	if f.Retry.Enabled && f.Retry.MaxAttempts < 1 {
		f.Retry.MaxAttempts = 3
	}
	if f.Scheduling.Enabled && f.Scheduling.Cron == "" {
		return fmt.Errorf("cron expression required when scheduling is enabled")
	}
	return nil
}

func (f *Function) validateWithFeatureRequired() error {
	if f.Feature == "" {
		return fmt.Errorf("feature is required")
	}
	return f.Validate()
}

func (c *Contract) validate() error {
	if c.Name == "" {
		return fmt.Errorf("name is required")
	}
	if err := ValidateRuntime(c.Runtime); err != nil {
		return err
	}
	if len(c.Functions) == 0 {
		return fmt.Errorf("at least one function is required")
	}
	for name, fn := range c.Functions {
		if fn == nil {
			return fmt.Errorf("function %q is required", name)
		}
		if err := fn.validateWithFeatureRequired(); err != nil {
			return fmt.Errorf("function %q: %w", name, err)
		}
	}
	return nil
}
