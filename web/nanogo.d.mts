/** Dependency-free browser integration for the nanoGo WASM worker. */
export interface WorkerMessage {
  type: string;
  [key: string]: unknown;
}
export interface Capabilities {
  hasFormat?: boolean;
  hasVet?: boolean;
  hasTests?: boolean;
  hasAst?: boolean;
  hasCallGraph?: boolean;
  hasBench?: boolean;
  hasWorkspace?: boolean;
  hasModuleCheck?: boolean;
  hasWorkspaceTests?: boolean;
  hasStructuredResults?: boolean;
  hasDiagnostics?: boolean;
  [key: string]: unknown;
}
export interface ClientOptions {
  /** Defaults to wasm_worker.js next to nanogo.mjs. */
  workerURL?: string | URL;
  /** Creates a new, dedicated worker. The client owns termination. Mutually
   * exclusive with workerURL. A handle can release host-owned object URLs. */
  workerFactory?: () => Worker | WorkerHandle;
  /** Supplied bytes are copied without detaching the host buffer. Exactly
   * one of wasmBytes, wasmModule, and wasmURL may be specified. */
  wasmBytes?: ArrayBuffer | ArrayBufferView;
  /** Compiled modules are cloned to the worker; instances/memory stay private. */
  wasmModule?: WebAssembly.Module;
  /** Skip importScripts: the factory must already install the matching Go runtime. */
  wasmExecProvided?: boolean;
  /** Require supplied WASM, runtime, and workerFactory; disable URL fallbacks. */
  offline?: boolean;
  /** Relative URLs resolve against the worker URL. Match the Go toolchain. */
  wasmURL?: string;
  wasmExecURL?: string;
  /** Includes download and startup; default 30000 milliseconds. */
  initTimeoutMs?: number;
  /** Per-dispatched-operation deadline; 0 disables it (default).
   * On timeout the worker is terminated and all unfinished operations reject. */
  requestTimeoutMs?: number;
  /** Receives individual messages, with batches already expanded. */
  onMessage?: (message: WorkerMessage) => void;
}
export interface WorkerHandle {
  worker: Worker;
  wasmExecProvided?: boolean;
  /** Called once after termination, including initialization failure. */
  dispose?: () => void;
}
/** Stable codes and phase describe failure without parsing Error.message. */
export interface Diagnostic {
  code: string;
  phase: 'parse' | 'load' | 'runtime' | 'cancel' | 'limit' | 'host';
  message: string;
  /** Lines and columns are 1-based; columns count UTF-8 bytes, not UTF-16 code units. */
  location?: { file: string; line: number; column: number };
  stack?: Array<{ function: string; location: { file: string; line: number; column: number } }>;
  limit?: { resource: string; maximum: number; used: number };
}
export interface StructuredResult { name: string; value: unknown }
export interface RunOptions {
  mode?: 'stream' | 'deferred';
  trace?: boolean;
  profile?: boolean;
  breakpoints?: number[];
  /** JSON-compatible data only; functions, cycles, nonfinite numbers, and
   * nesting beyond 64 levels are rejected. Requires a current WASM build. */
  inputs?: Record<string, unknown>;
  limits?: ResourceLimits;
}
export interface ResourceLimits {
  maxSteps?: number;
  maxGoroutines?: number;
  maxCallDepth?: number;
  maxOutputBytes?: number;
  maxAllocationUnits?: number;
  maxResultBytes?: number;
}
export interface RunStats {
  elapsedMs?: number;
  steps?: number;
  error?: string;
  diagnostic?: Diagnostic;
  results?: StructuredResult[];
  resultsCommitted?: boolean;
  [key: string]: unknown;
}
export interface RunResponse extends WorkerMessage {
  type: 'done' | 'workspace-done';
  elapsed?: number;
  stats: RunStats | null;
  error?: string;
  diagnostic?: Diagnostic;
}
export interface FormatResponse extends WorkerMessage {
  type: 'format-result';
  source: string;
}
export interface VetResponse extends WorkerMessage {
  type: 'vet-result';
  issues: Array<{ line?: number; column?: number; message?: string }>;
}
export interface ResultResponse extends WorkerMessage {
  type: 'test-result' | 'workspace-test-result' | 'workspace-check-result';
  result: Record<string, unknown>;
}
export interface WorkspaceFile { path: string; source: string }
export interface WorkspaceOptions { modulePath?: string }
export interface NanoGoError extends Error { response?: WorkerMessage; diagnostic?: Diagnostic }
export interface NanoGoClient {
  /** 2 for current workers; 1 for URL-based legacy workers without a version. */
  protocolVersion: number;
  capabilities: Capabilities;
  ready: Promise<NanoGoClient>;
  run(source: string, options?: RunOptions): Promise<RunResponse>;
  format(source: string): Promise<FormatResponse>;
  vet(source: string): Promise<VetResponse>;
  test(source: string, options?: { filter?: string }): Promise<ResultResponse>;
  checkWorkspace(files: WorkspaceFile[], options?: WorkspaceOptions): Promise<ResultResponse>;
  runWorkspace(files: WorkspaceFile[], options?: WorkspaceOptions & Pick<RunOptions, 'trace' | 'profile' | 'inputs' | 'limits'>): Promise<RunResponse>;
  testWorkspace(files: WorkspaceFile[], options?: WorkspaceOptions & { filter?: string }): Promise<ResultResponse>;
  /** Stops the worker and rejects every unfinished operation. */
  dispose(): void;
}
/** Operations reject on runtime/protocol errors; failed tests resolve normally. */
export function createNanoGo(options?: ClientOptions): Promise<NanoGoClient>;
export const PROTOCOL_VERSION: 2;
/** Executes trusted runtime and worker source in a Blob worker; no eval or
 * imports. Each call owns a fresh worker and an object URL until disposal. */
export function createInlineWorkerFactory(assets: {
  workerSource: string;
  wasmExecSource: string;
}): () => WorkerHandle;
