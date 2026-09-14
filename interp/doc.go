// Package interp executes a supported subset of Go source in native hosts and
// WebAssembly. It interprets an AST; it does not invoke the Go compiler or
// provide complete Go type checking or the complete standard library.
//
// Create a VM with NewInterpreter, enable curated packages with
// RegisterBuiltinPackages, and connect output with RegisterNative("ConsoleLog",
// ...). Formatting has a built-in implementation; registering __hostSprintf is
// only necessary to override it. Run executes one package-main source file;
// RunContext adds host cancellation and deadlines. Use interp/loader for
// programs spanning multiple files or packages.
//
// An interpreter retains globals, natives and package state across runs. Use a
// fresh interpreter per independent request and configure it before execution.
// Calls to RunContext on one interpreter are serialized; host callbacks used by
// guest goroutines must synchronize access to shared host state. Blocking host
// operations should use RegisterNativeContext and respect its context.
//
// Filesystem and network capabilities are denied by default. ExecutionLimits
// bounds interpreter work and guest goroutines, while MaxContainerSize bounds
// supported guest containers. These cooperative limits do not restrict arbitrary
// code inside host callbacks. LastStepCount reports the last run's deterministic
// evaluator checkpoints, rather than elapsed time or CPU instructions.
package interp
