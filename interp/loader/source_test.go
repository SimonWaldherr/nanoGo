package loader

import (
	"context"
	"testing"
)

func TestRunSourceTestsSplitsEditorDocument(t *testing.T) {
	vm, _ := newLoaderTestVM()
	results, err := RunSourceTests(context.Background(), vm, `package main

import "testing"

func Add(a, b int) int { return a + b }

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 { t.Fatalf("Add failed") }
}
`)
	if err != nil {
		t.Fatalf("RunSourceTests: %v", err)
	}
	if len(results) != 1 || !results[0].Pass {
		t.Fatalf("source test results = %+v, want one pass", results)
	}
	if _, err := vm.VFS.Stat("/tmp/nanogo-source-tests/main.go"); err == nil {
		t.Fatal("temporary source-test module was not removed")
	}
}

func TestRunSourceTestsReportsFailure(t *testing.T) {
	vm, _ := newLoaderTestVM()
	results, err := RunSourceTests(context.Background(), vm, `package main

import "testing"

func TestFailure(t *testing.T) { t.Errorf("expected failure") }
`)
	if err != nil {
		t.Fatalf("RunSourceTests: %v", err)
	}
	if len(results) != 1 || results[0].Pass || results[0].Category != CategoryWrongValue {
		t.Fatalf("source test results = %+v, want one failed assertion", results)
	}
}
