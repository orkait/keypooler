// Sample Go script for keypooler.
//
// Usage by keypooler:
//
//	go run trigger.go --function=greet --input='{"name":"world"}'
//
// The API key is available via KEYPOOLER_API_KEY env var.
// Output must be JSON to stdout: {"success": true, "data": {...}}
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
)

type result struct {
	Success bool   `json:"success"`
	Data    any    `json:"data,omitempty"`
	Error   string `json:"error,omitempty"`
}

func greet(input map[string]any) result {
	name, _ := input["name"].(string)
	if name == "" {
		name = "world"
	}

	apiKey := os.Getenv("KEYPOOLER_API_KEY")
	keyPreview := apiKey
	if len(apiKey) > 8 {
		keyPreview = apiKey[:8] + "..."
	}

	return result{
		Success: true,
		Data: map[string]any{
			"message":      fmt.Sprintf("Hello, %s!", name),
			"key_received": apiKey != "",
			"key_preview":  keyPreview,
		},
	}
}

var functions = map[string]func(map[string]any) result{
	"greet": greet,
}

func main() {
	functionName := flag.String("function", "", "function to call")
	inputJSON := flag.String("input", "{}", "JSON input")
	flag.Parse()

	if *functionName == "" {
		out, _ := json.Marshal(result{Error: "missing --function argument"})
		fmt.Println(string(out))
		os.Exit(1)
	}

	fn, ok := functions[*functionName]
	if !ok {
		out, _ := json.Marshal(result{Error: fmt.Sprintf("unknown function: %s", *functionName)})
		fmt.Println(string(out))
		os.Exit(1)
	}

	var input map[string]any
	if err := json.Unmarshal([]byte(*inputJSON), &input); err != nil {
		out, _ := json.Marshal(result{Error: fmt.Sprintf("invalid input JSON: %v", err)})
		fmt.Println(string(out))
		os.Exit(1)
	}

	res := fn(input)
	out, _ := json.Marshal(res)
	fmt.Println(string(out))
}
