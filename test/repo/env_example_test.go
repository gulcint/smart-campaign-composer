// Package repo_test checks repository files that the case document names explicitly.
package repo_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// repoRoot is relative to this package directory (test/repo).
const repoRoot = "../.."

func lines(t *testing.T, name string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		out = append(out, strings.TrimRight(l, "\r"))
	}
	return out
}

// REQ-AI-05
// "Uygulama key'i .env dosyasındaki ANTHROPIC_API_KEY değişkeninden okumalı; repoya bir
// .env.example ekle." The example declares an empty ANTHROPIC_API_KEY and holds no real key.
func TestEnvExample_DeclaresEmptyAnthropicKey(t *testing.T) {
	t.Parallel()
	ls := lines(t, ".env.example")
	var keyLines []string
	for _, l := range ls {
		if strings.Contains(l, "sk-ant-") {
			t.Fatalf(".env.example contains something that looks like an Anthropic key: %q", l)
		}
		if strings.HasPrefix(strings.TrimSpace(l), "ANTHROPIC_API_KEY") {
			keyLines = append(keyLines, strings.TrimSpace(l))
		}
	}
	if !slices.Equal(keyLines, []string{"ANTHROPIC_API_KEY="}) {
		t.Fatalf("ANTHROPIC_API_KEY lines in .env.example = %q, want exactly one empty assignment", keyLines)
	}
	if !slices.Contains(ls, "LLM_PROVIDER=auto") {
		t.Fatal(".env.example does not set LLM_PROVIDER=auto")
	}
}

// REQ-AI-05
// ".env repoya girmez": .env is ignored by git while .env.example stays tracked.
func TestGitignore_ExcludesDotEnv(t *testing.T) {
	t.Parallel()
	ls := lines(t, ".gitignore")
	for _, want := range []string{".env", "!.env.example"} {
		if !slices.Contains(ls, want) {
			t.Errorf(".gitignore has no %q line", want)
		}
	}
}
