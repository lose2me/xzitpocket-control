package ai

import (
	"strings"
	"testing"
)

func TestAppendPromptCalls(t *testing.T) {
	first := AppendPromptCalls("", []PromptCall{{Kind: "precheck", Messages: []PromptMessage{{Role: "user", Content: "a"}}}})
	if !strings.Contains(first, `"kind":"precheck"`) {
		t.Fatalf("first = %s", first)
	}
	second := AppendPromptCalls(first, []PromptCall{{Kind: "analyze", Messages: []PromptMessage{{Role: "user", Content: "b"}}}})
	if !strings.Contains(second, `"kind":"precheck"`) || !strings.Contains(second, `"kind":"analyze"`) {
		t.Fatalf("second = %s", second)
	}
}
