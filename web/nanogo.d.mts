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
  [key: string]: unknown;
}
export interface ClientOptions {
  /** Defaults to wasm_worker.js next to nanogo.mjs. */
  workerURL?: string | URL;
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
export interface RunOptions {
  mode?: 'stream' | 'deferred';
  trace?: boolean;
  profile?: boolean;
  breakpoints?: number[];
}
export interface RunStats {
  elapsedMs?: number;
  steps?: number;
  error?: string;
  [key: string]: unknown;
}
export interface RunResponse extends WorkerMessage {
  type: 'done' | 'workspace-done';
  elapsed?: number;
  stats: RunStats | null;
  error?: string;
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
export interface NanoGoError extends Error { response?: WorkerMessage }
export interface NanoGoClient {
  capabilities: Capabilities;
  ready: Promise<NanoGoClient>;
  run(source: string, options?: RunOptions): Promise<RunResponse>;
  format(source: string): Promise<FormatResponse>;
  vet(source: string): Promise<VetResponse>;
  test(source: string, options?: { filter?: string }): Promise<ResultResponse>;
  checkWorkspace(files: WorkspaceFile[], options?: WorkspaceOptions): Promise<ResultResponse>;
  runWorkspace(files: WorkspaceFile[], options?: WorkspaceOptions & Pick<RunOptions, 'trace' | 'profile'>): Promise<RunResponse>;
  testWorkspace(files: WorkspaceFile[], options?: WorkspaceOptions & { filter?: string }): Promise<ResultResponse>;
  /** Stops the worker and rejects every unfinished operation. */
  dispose(): void;
}
/** Operations reject on runtime/protocol errors; failed tests resolve normally. */
export function createNanoGo(options?: ClientOptions): Promise<NanoGoClient>;
