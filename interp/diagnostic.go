package interp

import (
	"context"
	"errors"
	"go/scanner"
)

// Diagnostic is the stable machine-readable view of an execution failure.
// Lines and columns are 1-based; columns count UTF-8 bytes (go/token), not UTF-16
// code units. Missing locations have zero fields. Codes never require parsing
// the human-readable Message, which may change between versions.
type Diagnostic struct {
	Code     string              `json:"code"`
	Phase    string              `json:"phase"`
	Message  string              `json:"message"`
	Location *DiagnosticLocation `json:"location,omitempty"`
	Stack    []DiagnosticFrame   `json:"stack,omitempty"`
	Limit    *LimitError         `json:"limit,omitempty"`
}
type DiagnosticLocation struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Column int    `json:"column"`
}
type DiagnosticFrame struct {
	Function string             `json:"function"`
	Location DiagnosticLocation `json:"location"`
}

// DiagnosticError preserves the original error chain and Error() text.
type DiagnosticError struct {
	Diagnostic Diagnostic
	Cause      error
}

func (e *DiagnosticError) Error() string { return e.Cause.Error() }
func (e *DiagnosticError) Unwrap() error { return e.Cause }
func diagnosticLocation(loc SourceLocation) *DiagnosticLocation {
	if loc.Line == 0 {
		return nil
	}
	return &DiagnosticLocation{loc.File, loc.Line, loc.Column}
}

// DiagnosticFor returns nil for success. phase is used only for errors without
// an intrinsic category (normally "load", "runtime", or "host").
func DiagnosticFor(err error, phase string) *Diagnostic {
	if err == nil {
		return nil
	}
	var wrapped *DiagnosticError
	if errors.As(err, &wrapped) {
		d := wrapped.Diagnostic
		return &d
	}
	if phase == "" {
		phase = "runtime"
	}
	d := &Diagnostic{Code: phase + ".error", Phase: phase, Message: err.Error()}
	var parse scanner.ErrorList
	var runtimeErr *RuntimeError
	var panicErr *panicError
	var limit *LimitError
	switch {
	case errors.As(err, &parse) && len(parse) > 0:
		d.Code = "parse.syntax"
		d.Phase = "parse"
		p := parse[0].Pos
		d.Location = &DiagnosticLocation{p.Filename, p.Line, p.Column}
	case errors.As(err, &limit):
		d.Code = "limit." + limit.Resource
		d.Phase = "limit"
		d.Limit = limit
	case errors.Is(err, ErrStepLimit):
		d.Code = "limit.steps"
		d.Phase = "limit"
	case errors.Is(err, ErrGoroutineLimit):
		d.Code = "limit.goroutines"
		d.Phase = "limit"
	case errors.Is(err, context.DeadlineExceeded):
		d.Code = "execution.deadline"
		d.Phase = "cancel"
	case errors.Is(err, ErrKilled):
		d.Code = "execution.killed"
		d.Phase = "cancel"
	case errors.Is(err, context.Canceled):
		d.Code = "execution.canceled"
		d.Phase = "cancel"
	case errors.Is(err, ErrHostContract):
		d.Code = "host.contract"
		d.Phase = "host"
	case errors.Is(err, ErrBridgeValue):
		d.Code = "host.value"
		d.Phase = "host"
	case errors.As(err, &runtimeErr):
		d.Code = "runtime.error"
		d.Phase = "runtime"
		d.Location = diagnosticLocation(runtimeErr.Loc)
		d.Stack = runtimeErr.Stack
	case errors.As(err, &panicErr):
		d.Code = "runtime.panic"
		d.Phase = "runtime"
		d.Location = diagnosticLocation(panicErr.Loc)
		d.Stack = panicErr.Stack
	}
	return d
}

// WithDiagnostic adds a phase to errors without destroying errors.Is/As.
func WithDiagnostic(err error, phase string) error {
	if err == nil {
		return nil
	}
	var existing *DiagnosticError
	if errors.As(err, &existing) {
		return err
	}
	return &DiagnosticError{Diagnostic: *DiagnosticFor(err, phase), Cause: err}
}
func (vm *Interpreter) executionDiagnostic(exec *execution, err error) error {
	if err == nil {
		return nil
	}
	// Preserve the long-standing concrete runtime error contract.
	switch err.(type) {
	case *RuntimeError, *panicError, scanner.ErrorList:
		return err
	}
	d := DiagnosticFor(err, "runtime")
	if d.Limit == nil {
		switch {
		case errors.Is(err, ErrStepLimit):
			d.Limit = &LimitError{Resource: "steps", Maximum: exec.limits.MaxSteps, Used: exec.steps.Load()}
		case errors.Is(err, ErrGoroutineLimit):
			d.Limit = &LimitError{Resource: "goroutines", Maximum: uint64(exec.limits.MaxGoroutines), Used: uint64(exec.goroutines.Load())}
		}
	}
	return &DiagnosticError{Diagnostic: *d, Cause: err}
}
func (vm *Interpreter) appendDiagnosticFrame(err error, fn *Function) {
	if err == nil {
		return
	}
	frame := DiagnosticFrame{Function: fn.Name}
	if fn.syntax != nil {
		loc := vm.traceLocation(fn.syntax.Pos())
		frame.Location = DiagnosticLocation{loc.File, loc.Line, loc.Column}
	}
	// Error paths retain at most 64 innermost frames regardless of recursion.
	var re *RuntimeError
	var pe *panicError
	if errors.As(err, &re) && len(re.Stack) < 64 {
		re.Stack = append(re.Stack, frame)
	}
	if errors.As(err, &pe) && len(pe.Stack) < 64 {
		pe.Stack = append(pe.Stack, frame)
	}
}
