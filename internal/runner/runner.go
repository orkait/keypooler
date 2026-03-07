package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"key-pool-system/internal/contract"
)

// Result is the parsed output from a script execution.
type Result struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// RuntimeStatus describes a single runtime's availability.
type RuntimeStatus struct {
	Name      string // "python3", "bun", "go"
	Available bool
	Version   string // version string if available
	Install   string // install instructions if missing
}

// Runner executes scripts either locally or inside a Docker container.
type Runner struct {
	Mode  string // resolved mode: "local" or "docker"
	Image string // docker image name (e.g. "keypooler-runtime")
}

// New creates a Runner. If mode is "docker" but Docker is unavailable, it
// falls back to "local". The returned warnings should be logged by the caller.
func New(mode, image string) (r *Runner, warnings []string) {
	r = &Runner{Image: image}

	switch mode {
	case "local":
		r.Mode = "local"
	case "auto":
		if dockerAvailable() {
			r.Mode = "docker"
		} else {
			r.Mode = "local"
			warnings = append(warnings, "Docker not found, falling back to local execution")
		}
	default: // "docker"
		if dockerAvailable() {
			r.Mode = "docker"
		} else {
			r.Mode = "local"
			warnings = append(warnings, "Docker not found, falling back to local execution")
		}
	}

	return r, warnings
}

// CheckLocalRuntimes probes the host for python3, bun, and go.
// Returns status for each runtime plus install commands for any that are missing.
func CheckLocalRuntimes() []RuntimeStatus {
	type probe struct {
		name string
		args []string // version check args differ per runtime
	}
	probes := []probe{
		{"python3", []string{"--version"}},
		{"bun", []string{"--version"}},
		{"go", []string{"version"}}, // go uses "go version", not "--version"
	}

	runtimes := make([]RuntimeStatus, len(probes))
	for i, p := range probes {
		runtimes[i] = RuntimeStatus{Name: p.name, Install: installHint(p.name)}
		out, err := exec.Command(p.name, p.args...).Output()
		if err == nil {
			runtimes[i].Available = true
			runtimes[i].Version = strings.TrimSpace(string(out))
			runtimes[i].Install = ""
		}
	}

	return runtimes
}

func installHint(name string) string {
	isWindows := runtime.GOOS == "windows"

	switch name {
	case "python3":
		if isWindows {
			return "winget install Python.Python.3 OR https://www.python.org/downloads/"
		}
		return "sudo apt install python3  (Debian/Ubuntu)\nbrew install python3       (macOS)"
	case "bun":
		if isWindows {
			return "powershell -c \"irm bun.sh/install.ps1 | iex\" OR npm install -g bun"
		}
		return "curl -fsSL https://bun.sh/install | bash"
	case "go":
		if isWindows {
			return "winget install GoLang.Go OR https://go.dev/dl/"
		}
		return "sudo apt install golang  (Debian/Ubuntu)\nbrew install go          (macOS)\nOR https://go.dev/dl/"
	default:
		return ""
	}
}

// Run executes a script function and returns the parsed result.
func (r *Runner) Run(ctx context.Context, c *contract.Contract, functionName, apiKey, inputJSON string) (*Result, error) {
	fn, ok := c.Functions[functionName]
	if !ok {
		return nil, fmt.Errorf("function %q not found in contract %q", functionName, c.Name)
	}

	execCtx, cancel := context.WithTimeout(ctx, fn.TimeoutDuration())
	defer cancel()

	var cmd *exec.Cmd
	if r.Mode == "docker" {
		cmd = r.dockerCmd(execCtx, c, functionName, apiKey, inputJSON)
	} else {
		cmd = r.localCmd(execCtx, c, functionName, apiKey, inputJSON)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("script timed out after %s", fn.TimeoutDuration())
		}
		return nil, fmt.Errorf("script failed: %s (stderr: %s)", err.Error(), stderr.String())
	}

	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse script output as JSON: %w (stdout: %s)", err, stdout.String())
	}

	return &result, nil
}

// localCmd builds a command that runs the script directly on the host.
func (r *Runner) localCmd(ctx context.Context, c *contract.Contract, functionName, apiKey, inputJSON string) *exec.Cmd {
	triggerPath := filepath.Join(c.Path, c.TriggerFile())
	args := c.RuntimeArgs()
	args = append(args, triggerPath,
		"--function="+functionName,
		"--input="+inputJSON,
	)

	cmd := exec.CommandContext(ctx, c.RuntimeBin(), args...)
	cmd.Dir = c.Path
	cmd.Env = []string{
		"KEYPOOLER_API_KEY=" + apiKey,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	return cmd
}

// dockerCmd builds a command that runs the script inside a Docker container.
// The script directory is mounted read-only at /workspace.
func (r *Runner) dockerCmd(ctx context.Context, c *contract.Contract, functionName, apiKey, inputJSON string) *exec.Cmd {
	triggerPath := "/workspace/" + c.TriggerFile()
	scriptArgs := c.RuntimeArgs()
	scriptArgs = append(scriptArgs, triggerPath,
		"--function="+functionName,
		"--input="+inputJSON,
	)

	dockerArgs := []string{
		"run", "--rm",
		"--network=none",
		"-e", "KEYPOOLER_API_KEY=" + apiKey,
		"-v", c.Path + ":/workspace:ro",
		"-w", "/workspace",
		r.Image,
		c.RuntimeBin(),
	}
	dockerArgs = append(dockerArgs, scriptArgs...)

	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	return cmd
}

// dockerAvailable checks if Docker is reachable.
var dockerAvailable = func() bool {
	err := exec.Command("docker", "info").Run()
	return err == nil
}
