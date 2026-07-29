package loader

import (
	"strings"
	"testing"
)

// TestParseModulePath exercises ParseModulePath's own edge cases directly,
// independent of LoadModule. Every existing loader test only ever feeds it
// perfectly-formed go.mod content (e.g. "module example.com/app\n"), so
// these cases cover the scanner/parsing behavior that would otherwise go
// completely untested: leading comments/blank lines, a missing module
// line, a trailing inline comment on the module line, and CRLF endings.
func TestParseModulePath(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		wantPath    string
		wantErrText string // substring expected in err.Error(); empty means no error expected
	}{
		{
			name:     "leading blank and comment lines before module",
			data:     "\n// this is a go.mod\n// see https://go.dev/ref/mod\n\nmodule foo/bar\n\ngo 1.21\n",
			wantPath: "foo/bar",
		},
		{
			name:        "missing module line returns an error",
			data:        "go 1.21\n",
			wantErrText: "no module line",
		},
		{
			name:     "trailing inline comment on the module line is ignored",
			data:     "module foo/bar // comment\n",
			wantPath: "foo/bar",
		},
		{
			name:     "CRLF line endings still parse correctly",
			data:     "// leading comment\r\nmodule foo/bar\r\n\r\ngo 1.21\r\n",
			wantPath: "foo/bar",
		},
		{
			name:     "well-formed module line still works",
			data:     "module example.com/app\n",
			wantPath: "example.com/app",
		},
		{
			name:        "empty input returns an error",
			data:        "",
			wantErrText: "no module line",
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseModulePath([]byte(tt.data))

			if tt.wantErrText != "" {
				if err == nil {
					t.Fatalf("ParseModulePath(%q) = %q, nil; want error containing %q", tt.data, got, tt.wantErrText)
				}
				if !strings.Contains(err.Error(), tt.wantErrText) {
					t.Fatalf("ParseModulePath(%q) error = %v; want it to contain %q", tt.data, err, tt.wantErrText)
				}
				return
			}

			if err != nil {
				t.Fatalf("ParseModulePath(%q) unexpected error: %v", tt.data, err)
			}
			if got != tt.wantPath {
				t.Errorf("ParseModulePath(%q) = %q, want %q", tt.data, got, tt.wantPath)
			}
		})
	}
}

func TestParseModuleFileLocalReplaces(t *testing.T) {
	mod, err := ParseModuleFile([]byte(`module example.com/app

replace example.com/one v1.2.3 => ./third_party/one
replace (
  example.com/two => ../two // an inline comment is allowed
  example.com/remote => example.com/mirror v1.0.0
)
`))
	if err != nil {
		t.Fatalf("ParseModuleFile: %v", err)
	}
	if got, want := mod.Path, "example.com/app"; got != want {
		t.Fatalf("module path = %q, want %q", got, want)
	}
	if got, want := mod.Replaces["example.com/one"], "./third_party/one"; got != want {
		t.Fatalf("single replace = %q, want %q", got, want)
	}
	if got, want := mod.Replaces["example.com/two"], "../two"; got != want {
		t.Fatalf("block replace = %q, want %q", got, want)
	}
	if _, ok := mod.Replaces["example.com/remote"]; ok {
		t.Fatal("remote replacement must not be treated as a VFS dependency")
	}
}
