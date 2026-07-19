package interp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSamplePrograms(t *testing.T) {
	paths, err := filepath.Glob("../samples/*.go")
	if err != nil {
		t.Fatalf("find sample programs: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no sample programs found")
	}

	for _, path := range paths {
		path := path
		t.Run(filepath.Base(path), func(t *testing.T) {
			source, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read sample: %v", err)
			}
			vm, _ := newTestVM()
			if err := vm.Run(string(source)); err != nil {
				t.Fatalf("sample program failed: %v", err)
			}
		})
	}
}
