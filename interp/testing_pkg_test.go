package interp

import (
	"context"
	"testing"
)

func TestTestingPackageErrorfFatalfAndRun(t *testing.T) {
	vm, _ := newTestVM()
	ctx := context.Background()

	src := `package mypkg

import "testing"

func TestAdd(t *testing.T) {
	if 2+2 != 5 {
		t.Errorf("got %d want %d", 4, 5)
	}
}

func TestFatal(t *testing.T) {
	t.Fatalf("boom %s", "now")
}

func TestWithSub(t *testing.T) {
	t.Run("child", func(t *testing.T) {
		t.Errorf("child failed")
	})
}
`
	if err := vm.VFS.MkdirAll("/tpkg", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := vm.VFS.WriteFile("/tpkg/a.go", []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	files, fset, err := ParsePackageDir(vm.VFS, "/tpkg")
	if err != nil {
		t.Fatalf("ParsePackageDir: %v", err)
	}

	ps := vm.NewPackageScope("mypkg")
	err = vm.WithExecution(ctx, fset, func() error {
		for _, f := range files {
			if err := ps.CollectDecls(f, fset); err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := ps.EvalDecls(ctx, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	// TestAdd: Errorf records failure but doesn't stop execution.
	{
		entry, ok := ps.Lookup("TestAdd")
		if !ok {
			t.Fatalf("TestAdd not declared")
		}
		fn := entry.(*Function)
		tv := vm.NewTestT()
		if _, err := vm.CallEntry(ctx, fset, fn, []any{tv}); err != nil {
			t.Fatalf("TestAdd: unexpected error %v", err)
		}
		if !TestFailed(tv) {
			t.Errorf("expected TestAdd to be marked failed")
		}
		msgs := TestMessages(tv)
		if len(msgs) != 1 || msgs[0] != "got 4 want 5" {
			t.Errorf("unexpected messages: %v", msgs)
		}
	}

	// TestFatal: Fatalf stops execution and surfaces the sentinel error.
	{
		entry, ok := ps.Lookup("TestFatal")
		if !ok {
			t.Fatalf("TestFatal not declared")
		}
		fn := entry.(*Function)
		tv := vm.NewTestT()
		_, err := vm.CallEntry(ctx, fset, fn, []any{tv})
		if !IsTestFatal(err) {
			t.Fatalf("expected IsTestFatal(err), got %v", err)
		}
		if !TestFailed(tv) {
			t.Errorf("expected TestFatal to be marked failed")
		}
	}

	// TestWithSub: a failing t.Run subtest propagates to the parent without
	// erroring the outer call (matching real testing.T.Run semantics).
	{
		entry, ok := ps.Lookup("TestWithSub")
		if !ok {
			t.Fatalf("TestWithSub not declared")
		}
		fn := entry.(*Function)
		tv := vm.NewTestT()
		if _, err := vm.CallEntry(ctx, fset, fn, []any{tv}); err != nil {
			t.Fatalf("TestWithSub: unexpected error %v", err)
		}
		if !TestFailed(tv) {
			t.Errorf("expected the parent test to be marked failed after a failing subtest")
		}
	}
}
