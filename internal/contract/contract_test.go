package contract

import "testing"

func TestNormalizeRuntimeSupportsRustboxLanguages(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"python":     "python",
		"py":         "python",
		"javascript": "javascript",
		"js":         "javascript",
		"typescript": "typescript",
		"ts":         "typescript",
		"cpp":        "cpp",
		"c++":        "cpp",
		"cxx":        "cpp",
		"cc":         "cpp",
		"rust":       "rust",
		"rs":         "rust",
		"java":       "java",
		"go":         "go",
		"c":          "c",
	}

	for input, expected := range cases {
		got, err := NormalizeRuntime(input)
		if err != nil {
			t.Fatalf("NormalizeRuntime(%q) error = %v", input, err)
		}
		if got != expected {
			t.Fatalf("NormalizeRuntime(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestNormalizeRuntimeRejectsOldRunnerOnlyRuntimes(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"node", "bun", "deno"} {
		if _, err := NormalizeRuntime(input); err == nil {
			t.Fatalf("NormalizeRuntime(%q) expected error", input)
		}
	}
}
