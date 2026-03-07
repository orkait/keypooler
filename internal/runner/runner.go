package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"key-pool-system/internal/contract"
)

// Result is the parsed output from a script execution.
type Result struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// Run executes a script function as a subprocess and returns the parsed result.
func Run(ctx context.Context, c *contract.Contract, functionName, apiKey, inputJSON string) (*Result, error) {
	fn, ok := c.Functions[functionName]
	if !ok {
		return nil, fmt.Errorf("function %q not found in contract %q", functionName, c.Name)
	}

	// Build timeout context from contract
	execCtx, cancel := context.WithTimeout(ctx, fn.TimeoutDuration())
	defer cancel()

	// Build command args — key passed via env to avoid leaking in process list
	triggerPath := filepath.Join(c.Path, c.TriggerFile())
	args := c.RuntimeArgs()
	args = append(args, triggerPath,
		"--function="+functionName,
		"--input="+inputJSON,
	)

	cmd := exec.CommandContext(execCtx, c.RuntimeBin(), args...)
	cmd.Dir = c.Path
	cmd.Env = []string{"KEYPOOLER_API_KEY=" + apiKey, "PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		// Check if it was a timeout
		if execCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("script timed out after %s", fn.TimeoutDuration())
		}
		return nil, fmt.Errorf("script failed: %s (stderr: %s)", err.Error(), stderr.String())
	}

	// Parse stdout as JSON
	var result Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return nil, fmt.Errorf("failed to parse script output as JSON: %w (stdout: %s)", err, stdout.String())
	}

	return &result, nil
}
