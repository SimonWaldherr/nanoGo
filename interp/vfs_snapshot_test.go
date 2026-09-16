package interp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
)

func TestVFSCloneIndependentWrites(t *testing.T) {
	for _, replacement := range []string{"", "x", "replaced", strings.Repeat("longer", 32)} {
		t.Run(fmt.Sprintf("bytes=%d", len(replacement)), func(t *testing.T) {
			original := NewVFS()
			if err := original.WriteFile("/tmp/data", []byte("original"), 0644); err != nil {
				t.Fatal(err)
			}
			first, sibling := original.Clone(), original.Clone()
			grandchild := first.Clone()
			for _, fs := range []*VFS{original, first} {
				data := []byte(replacement)
				if err := fs.WriteFile("/tmp/data", data, 0600); err != nil {
					t.Fatal(err)
				}
				// Public writes and reads must never expose the backing buffer.
				clear(data)
				got, err := fs.ReadFile("/tmp/data")
				if err != nil || string(got) != replacement {
					t.Fatalf("write: %q, %v", got, err)
				}
				clear(got)
				got, err = fs.ReadFile("/tmp/data")
				if err != nil || string(got) != replacement {
					t.Fatalf("read alias: %q, %v", got, err)
				}
			}
			for _, fs := range []*VFS{sibling, grandchild} {
				got, err := fs.ReadFile("/tmp/data")
				info, statErr := fs.Stat("/tmp/data")
				if err != nil || string(got) != "original" || statErr != nil || info.Mode != 0644 {
					t.Fatalf("snapshot changed: %q, %+v, %v, %v", got, info, err, statErr)
				}
			}
			// A zero-length rewrite must not retain a reusable shared buffer.
			if err := first.WriteFile("/tmp/data", []byte("again"), 0600); err != nil {
				t.Fatal(err)
			}
			got, _ := grandchild.ReadFile("/tmp/data")
			if string(got) != "original" {
				t.Fatalf("second write changed grandchild: %q", got)
			}
		})
	}
}

func TestVFSCloneImportsAndMetadata(t *testing.T) {
	fs := NewVFS()
	if err := fs.ImportReader(context.Background(), "/tmp/data", strings.NewReader("original"), 0); err != nil {
		t.Fatal(err)
	}
	if err := fs.MountFS("/assets", fstest.MapFS{"locked": &fstest.MapFile{Data: []byte("sealed")}}); err != nil {
		t.Fatal(err)
	}
	clone := fs.Clone()
	revision := clone.Revision()
	if err := fs.ImportReader(context.Background(), "/tmp/data", strings.NewReader("imported"), 0); err != nil {
		t.Fatal(err)
	}
	if err := clone.ImportFS("/tmp", fstest.MapFS{"data": &fstest.MapFile{Data: []byte("replaced")}}, VFSImportOptions{}); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct {
		fs   *VFS
		want string
	}{{fs, "imported"}, {clone, "replaced"}} {
		got, err := item.fs.ReadFile("/tmp/data")
		if err != nil || string(got) != item.want || item.fs.Revision() <= revision {
			t.Fatalf("import: %q, %v", got, err)
		}
		if err := item.fs.WriteFile("/assets/locked", []byte("bad"), 0600); err == nil {
			t.Fatal("clone lost read-only flags")
		}
	}
	fs.Setenv("USER", "changed")
	if err := fs.Chdir("/tmp"); err != nil {
		t.Fatal(err)
	}
	if err := fs.RemoveAll("/tmp"); err != nil {
		t.Fatal(err)
	}
	if clone.Getenv("USER") != "user" || clone.Getwd() != "/home/user" {
		t.Fatal("snapshot environment/cwd changed")
	}
	got, err := clone.ReadFile("/tmp/data")
	if err != nil || string(got) != "replaced" {
		t.Fatalf("snapshot removed: %q, %v", got, err)
	}
}

func TestVFSCloneConcurrentSnapshots(t *testing.T) {
	fs := NewVFS()
	if err := fs.WriteFile("/tmp/data", bytes.Repeat([]byte{'a'}, 4096), 0644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte('a' + worker)}, 4096)
			for i := 0; i < 30; i++ {
				snapshot := fs.Clone()
				before, err := snapshot.ReadFile("/tmp/data")
				if err != nil {
					t.Error(err)
					return
				}
				if err := fs.WriteFile("/tmp/data", data, 0600); err != nil {
					t.Error(err)
					return
				}
				after, err := snapshot.ReadFile("/tmp/data")
				if err != nil || !bytes.Equal(before, after) || !bytes.Equal(before, bytes.Repeat(before[:1], len(before))) {
					t.Errorf("torn or mutable snapshot: %v", err)
					return
				}
				if err := snapshot.WriteFile("/tmp/data", data, 0600); err != nil {
					t.Error(err)
					return
				}
			}
		}(worker)
	}
	wg.Wait()
}

func BenchmarkVFSClone(b *testing.B) {
	for _, size := range []int{0, 1 << 20, 16 << 20} {
		b.Run(fmt.Sprintf("AssetBytes=%d", size), func(b *testing.B) {
			fs := NewVFS()
			if err := fs.WriteFile("/tmp/asset", make([]byte, size), 0644); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				snapshot := fs.Clone()
				if snapshot.Revision() != fs.Revision() {
					b.Fatal("revision changed")
				}
			}
		})
	}
}
