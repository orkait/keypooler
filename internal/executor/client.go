package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"key-pool-system/internal/contract"
	"key-pool-system/internal/db"
)

// Result is the structured output expected from integration code.
type Result struct {
	Success bool            `json:"success"`
	Data    json.RawMessage `json:"data"`
	Error   string          `json:"error"`
}

// Client executes single-file integration code via the local rustbox HTTP API.
type Client struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func NewRustboxClient(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

type submitRequest struct {
	Language string `json:"language"`
	Code     string `json:"code"`
	Stdin    string `json:"stdin"`
}

type resultEnvelope struct {
	ID           string `json:"id"`
	JobStatus    string `json:"job_status"`
	Verdict      string `json:"verdict"`
	Stdout       string `json:"stdout"`
	Stderr       string `json:"stderr"`
	ErrorMessage string `json:"error_message"`
}

type stdinEnvelope struct {
	Integration string          `json:"integration"`
	Function    string          `json:"function"`
	Version     int             `json:"version"`
	APIKey      string          `json:"api_key"`
	Input       json.RawMessage `json:"input"`
}

func (c *Client) Execute(ctx context.Context, version *db.IntegrationVersion, fn *contract.Function, selectedKey, inputJSON string) (*Result, error) {
	if version == nil {
		return nil, fmt.Errorf("integration version is required")
	}

	if inputJSON == "" {
		inputJSON = "{}"
	}

	execCtx := ctx
	if fn != nil {
		if timeout := fn.TimeoutDuration(); timeout > 0 {
			var cancel context.CancelFunc
			execCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
	}

	stdinBytes, err := json.Marshal(stdinEnvelope{
		Integration: version.IntegrationName,
		Function:    version.FunctionName,
		Version:     version.Version,
		APIKey:      selectedKey,
		Input:       json.RawMessage(inputJSON),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode stdin envelope: %w", err)
	}

	reqBody, err := json.Marshal(submitRequest{
		Language: version.Runtime,
		Code:     version.Code,
		Stdin:    string(stdinBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to encode rustbox request: %w", err)
	}

	result, err := c.submitWait(execCtx, reqBody)
	if err != nil {
		return nil, err
	}
	return parseProgramResult(result)
}

func (c *Client) submitWait(ctx context.Context, body []byte) (*resultEnvelope, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/submit?wait=true", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create rustbox request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", c.apiKey)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("rustbox submit failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestTimeout {
		var timeoutResp struct {
			ID string `json:"id"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&timeoutResp); err != nil {
			return nil, fmt.Errorf("rustbox wait timeout decode failed: %w", err)
		}
		if timeoutResp.ID == "" {
			return nil, fmt.Errorf("rustbox wait timed out without submission id")
		}
		return c.pollResult(ctx, timeoutResp.ID)
	}

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&apiErr)
		if apiErr.Error == "" {
			apiErr.Error = resp.Status
		}
		return nil, fmt.Errorf("rustbox submit error: %s", apiErr.Error)
	}

	var result resultEnvelope
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode rustbox result: %w", err)
	}
	return &result, nil
}

func (c *Client) pollResult(ctx context.Context, id string) (*resultEnvelope, error) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/result/"+id, nil)
			if err != nil {
				return nil, fmt.Errorf("failed to create rustbox poll request: %w", err)
			}
			req.Header.Set("X-API-Key", c.apiKey)

			resp, err := c.httpClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("rustbox poll failed: %w", err)
			}

			var result resultEnvelope
			decodeErr := json.NewDecoder(resp.Body).Decode(&result)
			resp.Body.Close()
			if decodeErr != nil {
				return nil, fmt.Errorf("failed to decode rustbox poll result: %w", decodeErr)
			}

			switch result.JobStatus {
			case "completed":
				return &result, nil
			case "error":
				if result.ErrorMessage != "" {
					return nil, fmt.Errorf("rustbox execution error: %s", result.ErrorMessage)
				}
				return nil, fmt.Errorf("rustbox execution error")
			}
		}
	}
}

func parseProgramResult(result *resultEnvelope) (*Result, error) {
	if result == nil {
		return nil, fmt.Errorf("rustbox result is required")
	}
	if result.JobStatus != "completed" {
		return nil, fmt.Errorf("rustbox job not completed: %s", result.JobStatus)
	}
	if result.Verdict != "AC" {
		msg := strings.TrimSpace(result.ErrorMessage)
		if msg == "" {
			msg = strings.TrimSpace(result.Stderr)
		}
		if msg == "" {
			msg = fmt.Sprintf("rustbox verdict %s", result.Verdict)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	var programResult Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &programResult); err != nil {
		return nil, fmt.Errorf("integration stdout is not valid JSON result: %w", err)
	}
	return &programResult, nil
}
