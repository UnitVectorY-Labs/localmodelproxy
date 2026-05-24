package main

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestBuildVersionOutput(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		prefix string
	}{
		{
			name:   "normalizes semver without v prefix",
			input:  "1.2.3",
			prefix: "v1.2.3 ",
		},
		{
			name:   "keeps semver with v prefix",
			input:  "v1.2.3",
			prefix: "v1.2.3 ",
		},
		{
			name:   "keeps non semver",
			input:  "dev",
			prefix: "dev ",
		},
	}

	suffix := fmt.Sprintf("(%s, %s/%s)", runtime.Version(), runtime.GOOS, runtime.GOARCH)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := buildVersionOutput(tt.input)
			if !strings.HasPrefix(out, tt.prefix) {
				t.Fatalf("expected prefix %q, got %q", tt.prefix, out)
			}
			if !strings.HasSuffix(out, suffix) {
				t.Fatalf("expected suffix %q, got %q", suffix, out)
			}
		})
	}
}
