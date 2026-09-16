// wasm_worker.js
// Runs the Go WASM in a Web Worker and forwards structured messages to the main thread.

let goReady = false;
let _initPromise;
let _runtimeFailure;
const PROTOCOL_VERSION = 2;
// Resolved by Go (via the `nanoGoSignalReady` host helper) once all
// `nanoGo*` globals have been registered. This avoids the previous
// busy-wait loop that polled every 10 ms for up to 3 seconds.
let _readyResolve;
const _readyPromise = new Promise((resolve) => { _readyResolve = resolve; });

// ---------------------------------------------------------------------------
// Outbound message batching.
//
// Guest programs can emit thousands of `log` messages in a tight loop. One
// postMessage per event means one structured clone + one main-thread task
// each, which dominates total run time. High-frequency message types are
// therefore buffered and shipped as a single `{type:'batch', items:[...]}`
// message: on flush points (canvas-frame, run end), when the buffer is full,
// or on the next microtask once the synchronous Go slice yields (covers
// animated demos that time.Sleep between frames).
// ---------------------------------------------------------------------------
const BATCHABLE = { 'log': 1, 'warn': 1, 'canvas-size': 1, 'canvas-frame': 1 };
const BATCH_LIMIT = 2048;
let _batch = [];
let _batchScheduled = false;
// The full workspace source snapshot is expensive to structured-clone for
// every Check/Test/Run. Keep the latest immutable message copy in the worker;
// each Go call below still builds a fresh private VFS from it, so this cache
// saves wire traffic without sharing guest state between executions.
let _workspaceSnapshot = null;

// canvas-frame owns a freshly allocated Uint8Array (see CanvasBinding.Flush
// in runtime/native_std.go), so its backing buffer can move to the window
// without invalidating WASM memory. Structured cloning a large grid used to
// make an extra full-size copy for every animation frame. Keep this narrowly
// scoped to whole, transferable ArrayBuffers: a view into a larger buffer
// must remain cloneable because transferring it would detach unrelated data.
function guestTransferables(payload) {
  const items = payload && payload.type === 'batch' ? payload.items : [payload];
  const transfers = [];
  for (const item of items) {
    if (!item || item.type !== 'canvas-frame') continue;
    const cells = item.cells;
    if (cells && cells.buffer instanceof ArrayBuffer &&
        cells.byteOffset === 0 && cells.byteLength === cells.buffer.byteLength) {
      transfers.push(cells.buffer);
    }
  }
  return transfers;
}

// `post` is usually self.postMessage, but deferred runs temporarily replace
// it with an in-memory collector. Passing a transfer list to that collector
// is harmless (it ignores the second argument); the buffer is transferred
// only when the collector is replayed through the real postMessage below.
function postGuestPayload(post, payload) {
  const transfers = guestTransferables(payload);
  if (transfers.length > 0) {
    post.call(self, payload, transfers);
  } else {
    post.call(self, payload);
  }
}

function flushBatch() {
  _batchScheduled = false;
  if (_batch.length === 0) return;
  const items = _batch;
  _batch = [];
  if (items.length === 1) {
    postGuestPayload(self.postMessage, items[0]);
  } else {
    postGuestPayload(self.postMessage, { type: 'batch', items: items });
  }
}

function postFromGuest(msg) {
  const t = msg && msg.type;
  if (t && BATCHABLE[t]) {
    _batch.push(msg);
    // canvas-frame is an explicit guest flush. Deliver it immediately so an
    // animation's frame order survives even when Go yields faster than the
    // browser's next paint.
    if (t === 'canvas-frame' || _batch.length >= BATCH_LIMIT) {
      flushBatch();
    } else if (!_batchScheduled) {
      _batchScheduled = true;
      // Microtasks run whenever the Go scheduler yields to the event loop
      // (time.Sleep, channel waits), so animations stay live while tight
      // loops still coalesce into large batches.
      Promise.resolve().then(flushBatch);
    }
    return;
  }
  // Low-frequency / ordering-sensitive messages (dom-*, error, alert…):
  // flush pending batched output first to preserve global ordering.
  flushBatch();
  postGuestPayload(self.postMessage, msg);
}

function postConsoleFromGuest(type, text) {
  postFromGuest({ type, text });
}

// parseStats parses the JSON string a stats-aware WASM build returns from
// nanoGoRun/nanoGoAst/nanoGoBench. Older builds return undefined -> null.
function parseStats(raw) {
  if (typeof raw !== 'string' || raw.length === 0) return null;
  try { return JSON.parse(raw); } catch (e) { return null; }
}

function cacheWorkspaceSnapshot(msg) {
  if (!msg || !Array.isArray(msg.files)) return false;
  _workspaceSnapshot = {
    revision: Number(msg.workspaceRevision),
    files: msg.files,
    modulePath: String(msg.modulePath || '')
  };
  return true;
}

// Legacy/custom worker clients may still send files on every operation. The
// playground sends a prior workspace-sync and only a revision here.
function workspaceSnapshotFor(msg) {
  if (msg && Array.isArray(msg.files)) {
    return { files: msg.files, modulePath: String(msg.modulePath || '') };
  }
  if (_workspaceSnapshot && msg && _workspaceSnapshot.revision === Number(msg.workspaceRevision)) {
    return _workspaceSnapshot;
  }
  return null;
}

function workspaceInput(snapshot) {
  // Negotiate with the loaded binary: older WASM builds expect an array.
  if (self.nanoGoWorkspaceJSON !== true) return snapshot.files;
  if (snapshot.json === undefined) snapshot.json = JSON.stringify(snapshot.files);
  return snapshot.json;
}

// Only JSON data crosses this explicit host boundary. JSON.stringify alone
// would silently omit functions/undefined and change NaN into null.
function executionOptions(msg) {
  if (msg.inputs === undefined && msg.limits === undefined) return undefined;
  if (!getCapabilities().hasStructuredResults) throw new Error('This WASM build does not support inputs and limits');
  const visiting = new Set();
  function validate(value, depth) {
    if (depth > 64) throw new TypeError('Execution options exceed maximum nesting depth 64');
    if (value === null || typeof value === 'string' || typeof value === 'boolean') return;
    if (typeof value === 'number' && Number.isFinite(value)) return;
    if (typeof value !== 'object' || value === null) throw new TypeError('Execution options must contain only JSON values');
    if (visiting.has(value)) throw new TypeError('Execution options contain a cycle');
    if (!Array.isArray(value) && Object.getPrototypeOf(value) !== Object.prototype && Object.getPrototypeOf(value) !== null) {
      throw new TypeError('Execution options require plain objects and arrays');
    }
    visiting.add(value);
    for (const child of Array.isArray(value) ? value : Object.values(value)) validate(child, depth + 1);
    visiting.delete(value);
  }
  const options = {
    ...(msg.inputs !== undefined ? {inputs:msg.inputs} : {}),
    ...(msg.limits !== undefined ? {limits:msg.limits} : {})
  };
  validate(options, 0);
  return JSON.stringify(options);
}

self.onmessage = async function (ev) {
  const msg = ev.data;
  if (msg && msg.type === 'init') {
    try {
      await initWasmWorker(msg);
      self.postMessage({ type: 'ready', protocolVersion: PROTOCOL_VERSION, capabilities: getCapabilities() });
    } catch (e) {
      const text = 'WASM init failed: ' + (e && e.message ? e.message : String(e));
      self.postMessage({ type: 'error', fatal: true, text, diagnostic:{code:'host.initialization',phase:'host',message:text} });
    }
    return;
  }
  if (msg && msg.type === 'run') {
    if (!goReady) {
      self.postMessage({ type: 'error', text: 'WASM not initialized' });
      return;
    }
    const t0 = Date.now();
    let stats = null;
    let error;
    try {
      const options = executionOptions(msg);
      if (msg.mode === 'deferred') {
        // Buffer all worker->host messages until the run completes,
        // then flush them in order. Wrap in try/finally so a thrown
        // error in user code can't leave postMessage permanently
        // replaced (which would silently swallow future messages).
        const origPost = self.postMessage;
        const buffer = [];
        self.postMessage = function (m) { buffer.push(m); };
        try {
          stats = parseStats(self.nanoGoRun(msg.source, !!msg.trace, !!msg.profile, msg.breakpoints || [], options));
        } finally {
          flushBatch();
          self.postMessage = origPost;
          for (const m of buffer) { postGuestPayload(origPost, m); }
        }
      } else {
        stats = parseStats(self.nanoGoRun(msg.source, !!msg.trace, !!msg.profile, msg.breakpoints || [], options));
        flushBatch();
      }
    } catch (err) {
      flushBatch();
      error = String(err);
      self.postMessage({ type: 'error', text: error });
    }
    // Every initialized run has exactly one terminal response, after all
    // output (including thrown JS errors). Promise clients rely on this to
    // advance their queue without mixing consecutive executions.
    self.postMessage({ type: 'done', elapsed: Date.now() - t0, stats,
      ...(error ? { error, diagnostic:{code:'host.execution',phase:'host',message:error} } : {}) });
    return;
  }
  if (msg && msg.type === 'workspace-sync') {
    if (!cacheWorkspaceSnapshot(msg)) {
      self.postMessage({ type: 'workspace-sync-result', error: 'workspace sync requires a files array' });
    }
    return;
  }
  if (msg && msg.type === 'debug-start') {
    if (!goReady || typeof self.nanoGoDebugStart !== 'function') {
      self.postMessage({ type: 'debug-done', error: 'live debugging is not available in this WASM build' });
      return;
    }
    try {
      self.nanoGoDebugStart(msg.source || '', msg.breakpoints || []);
    } catch (err) {
      self.postMessage({ type: 'debug-done', error: String(err) });
    }
    return;
  }
  if (msg && msg.type === 'debug-command') {
    // Each command directly calls the matching WASM export. These calls are
    // synchronous but cheap (a map lookup and a channel send, or — for
    // set-variable — one expression evaluation) — the guest goroutine that's
    // actually parked mid-statement resumes independently, via the Go
    // scheduler, once the resume channel it's blocked on receives a value.
    if (msg.command === 'set-variable') {
      if (typeof self.nanoGoDebugSetVariable !== 'function') {
        self.postMessage({ type: 'debug-command-result', command: msg.command, ok: false, error: 'live debugging is not available in this WASM build' });
        return;
      }
      try {
        const result = self.nanoGoDebugSetVariable(msg.token || 0, String(msg.name || ''), String(msg.value || ''));
        self.postMessage({ type: 'debug-command-result', command: msg.command, ok: !!(result && result.ok), value: result && result.value, error: result && result.error, name: msg.name });
      } catch (err) {
        self.postMessage({ type: 'debug-command-result', command: msg.command, ok: false, error: String(err), name: msg.name });
      }
      return;
    }
    const fn = {
      continue: self.nanoGoDebugContinue,
      'step-into': self.nanoGoDebugStepInto,
      'step-over': self.nanoGoDebugStepOver,
      'step-out': self.nanoGoDebugStepOut,
      pause: self.nanoGoDebugPause,
      'set-breakpoints': self.nanoGoDebugSetBreakpoints,
      stop: self.nanoGoDebugStop
    }[msg.command];
    if (typeof fn !== 'function') {
      self.postMessage({ type: 'debug-command-result', command: msg.command, ok: false, error: 'unknown or unavailable debug command' });
      return;
    }
    try {
      const ok = msg.command === 'set-breakpoints' ? fn(msg.breakpoints || []) : fn(msg.token || 0);
      self.postMessage({ type: 'debug-command-result', command: msg.command, ok: !!ok });
    } catch (err) {
      self.postMessage({ type: 'debug-command-result', command: msg.command, ok: false, error: String(err) });
    }
    return;
  }
  if (msg && msg.type === 'workspace-run') {
    if (!goReady || typeof self.nanoGoRunWorkspace !== 'function') {
      self.postMessage({ type: 'workspace-done', error: 'multi-file workspace execution is not available in this WASM build' });
      return;
    }
    const t0 = Date.now();
    try {
      const snapshot = workspaceSnapshotFor(msg);
      if (!snapshot) {
        self.postMessage({ type: 'workspace-done', error: 'workspace snapshot is unavailable; sync it before running' });
        return;
      }
      const stats = parseStats(self.nanoGoRunWorkspace(workspaceInput(snapshot), snapshot.modulePath, !!msg.trace, !!msg.profile, executionOptions(msg)));
      flushBatch();
      if (!stats) {
        self.postMessage({ type: 'workspace-done', error: 'workspace run returned no data' });
      } else {
        self.postMessage({ type: 'workspace-done', elapsed: Date.now() - t0, stats });
      }
    } catch (err) {
      flushBatch();
      self.postMessage({ type: 'workspace-done', error: String(err), diagnostic:{code:'host.execution',phase:'host',message:String(err)} });
    }
    return;
  }
  if (msg && msg.type === 'workspace-check') {
    if (!goReady || typeof self.nanoGoWorkspaceCheck !== 'function') {
      self.postMessage({ type: 'workspace-check-result', error: 'workspace check is not available in this WASM build' });
      return;
    }
    try {
      const snapshot = workspaceSnapshotFor(msg);
      if (!snapshot) {
        self.postMessage({ type: 'workspace-check-result', error: 'workspace snapshot is unavailable; sync it before checking' });
        return;
      }
      const result = parseStats(self.nanoGoWorkspaceCheck(workspaceInput(snapshot), snapshot.modulePath));
      if (!result) {
        self.postMessage({ type: 'workspace-check-result', error: 'workspace check returned no data' });
      } else if (result.error) {
        self.postMessage({ type: 'workspace-check-result', error: result.error, result });
      } else {
        self.postMessage({ type: 'workspace-check-result', result });
      }
    } catch (err) {
      self.postMessage({ type: 'workspace-check-result', error: String(err) });
    }
    return;
  }
  if (msg && msg.type === 'workspace-test') {
    if (!goReady || typeof self.nanoGoTestWorkspace !== 'function') {
      self.postMessage({ type: 'workspace-test-result', error: 'multi-file workspace tests are not available in this WASM build' });
      return;
    }
    try {
      const snapshot = workspaceSnapshotFor(msg);
      if (!snapshot) {
        self.postMessage({ type: 'workspace-test-result', error: 'workspace snapshot is unavailable; sync it before testing' });
        return;
      }
      const result = parseStats(self.nanoGoTestWorkspace(workspaceInput(snapshot), snapshot.modulePath, String(msg.filter || '')));
      if (!result) {
        self.postMessage({ type: 'workspace-test-result', error: 'workspace tests returned no data' });
      } else if (result.error) {
        self.postMessage({ type: 'workspace-test-result', error: result.error, result });
      } else {
        self.postMessage({ type: 'workspace-test-result', result });
      }
    } catch (err) {
      self.postMessage({ type: 'workspace-test-result', error: String(err) });
    }
    return;
  }
  if (msg && msg.type === 'ast') {
    if (!goReady || typeof self.nanoGoAst !== 'function') {
      self.postMessage({ type: 'ast-result', error: 'AST inspection not available in this WASM build' });
      return;
    }
    try {
      const res = parseStats(self.nanoGoAst(msg.source || ''));
      if (!res) {
        self.postMessage({ type: 'ast-result', error: 'AST inspection returned no data' });
      } else if (res.error) {
        self.postMessage({ type: 'ast-result', error: res.error, result:res });
      } else {
        self.postMessage({ type: 'ast-result', result: res });
      }
    } catch (e) {
      self.postMessage({ type: 'ast-result', error: String(e) });
    }
    return;
  }
  if (msg && msg.type === 'callgraph') {
    if (!goReady || typeof self.nanoGoCallGraph !== 'function') {
      self.postMessage({ type: 'callgraph-result', error: 'call graph not available in this WASM build' });
      return;
    }
    try {
      const res = parseStats(self.nanoGoCallGraph(msg.source || ''));
      if (!res) {
        self.postMessage({ type: 'callgraph-result', error: 'call graph returned no data' });
      } else if (res.error) {
        self.postMessage({ type: 'callgraph-result', error: res.error, result:res });
      } else {
        self.postMessage({ type: 'callgraph-result', result: res });
      }
    } catch (e) {
      self.postMessage({ type: 'callgraph-result', error: String(e) });
    }
    return;
  }
  if (msg && msg.type === 'bench') {
    if (!goReady || typeof self.nanoGoBench !== 'function') {
      self.postMessage({ type: 'bench-result', error: 'benchmark not available in this WASM build' });
      return;
    }
    // Silence guest output while benchmarking: the same program runs many
    // times and its prints/canvas writes would flood the host. Count what
    // was suppressed so the UI can say so.
    const origHook = self.nanoGoPostMessage;
    let suppressed = 0;
    self.nanoGoPostMessage = function (m) {
      const t = m && m.type;
      if (t === 'error') { origHook(m); return; }
      suppressed++;
    };
    try {
      const res = parseStats(self.nanoGoBench(msg.source || '', Number(msg.iterations) | 0, !!msg.profile));
      if (!res) {
        self.postMessage({ type: 'bench-result', error: 'benchmark returned no data' });
      } else if (res.error) {
        self.postMessage({ type: 'bench-result', error: res.error, result: res });
      } else {
        res.suppressedMessages = suppressed;
        self.postMessage({ type: 'bench-result', result: res });
      }
    } catch (e) {
      self.postMessage({ type: 'bench-result', error: String(e) });
    } finally {
      self.nanoGoPostMessage = origHook;
    }
    return;
  }
  if (msg && msg.type === 'format') {
    if (!goReady || typeof self.nanoGoFormat !== 'function') {
      self.postMessage({ type: 'format-result', error: 'format not available in this WASM build' });
      return;
    }
    try {
      const result = self.nanoGoFormat(msg.source || '');
      if (result && typeof result.error === 'string') {
        self.postMessage({ type: 'format-result', error: result.error, diagnostic:result.diagnostic });
      } else {
        self.postMessage({ type: 'format-result', source: result && result.source != null ? result.source : msg.source });
      }
    } catch (e) {
      self.postMessage({ type: 'format-result', error: String(e) });
    }
    return;
  }
  if (msg && msg.type === 'test') {
    if (!goReady || typeof self.nanoGoTest !== 'function') {
      self.postMessage({ type: 'test-result', error: 'tests not available in this WASM build' });
      return;
    }
    try {
      const result = parseStats(self.nanoGoTest(msg.source || '', String(msg.filter || '')));
      if (!result) {
        self.postMessage({ type: 'test-result', error: 'test runner returned no result' });
      } else if (result.error) {
        self.postMessage({ type: 'test-result', error: result.error, result });
      } else {
        self.postMessage({ type: 'test-result', result });
      }
    } catch (e) {
      self.postMessage({ type: 'test-result', error: String(e) });
    }
    return;
  }
  if (msg && msg.type === 'vet') {
    if (!goReady || typeof self.nanoGoVet !== 'function') {
      self.postMessage({ type: 'vet-result', error: 'vet not available in this WASM build' });
      return;
    }
    try {
      const raw = self.nanoGoVet(msg.source || '');
      // raw may be a JS array-like or an error object
      if (raw && typeof raw.error === 'string') {
        self.postMessage({ type: 'vet-result', error: raw.error, diagnostic:raw.diagnostic });
        return;
      }
      const issues = [];
      const len = raw && typeof raw.length === 'number' ? raw.length : 0;
      for (let i = 0; i < len; i++) {
        const iss = raw[i];
        issues.push({
          line: iss ? iss.line : undefined,
          column: iss ? iss.column : undefined,
          message: iss ? iss.message : undefined
        });
      }
      self.postMessage({ type: 'vet-result', issues });
    } catch (e) {
      self.postMessage({ type: 'vet-result', error: String(e) });
    }
    return;
  }
};

function getCapabilities() {
  try {
    if (typeof self.nanoGoVersion === 'function') {
      const info = parseStats(self.nanoGoVersion());
      if (info) return info;
    }
  } catch (e) {/*no-op*/}
  return {
    hasFormat: typeof self.nanoGoFormat === 'function',
    hasVet: typeof self.nanoGoVet === 'function',
    hasTests: typeof self.nanoGoTest === 'function',
    hasAst: typeof self.nanoGoAst === 'function',
    hasCallGraph: typeof self.nanoGoCallGraph === 'function',
    hasBench: typeof self.nanoGoBench === 'function',
    hasWorkspace: typeof self.nanoGoRunWorkspace === 'function',
    hasModuleCheck: typeof self.nanoGoWorkspaceCheck === 'function',
    hasWorkspaceTests: typeof self.nanoGoTestWorkspace === 'function'
  };
}

async function instantiateWasm(url, imports) {
  const response = await fetch(url);
  if (!response.ok) throw new Error('WASM fetch failed: HTTP ' + response.status);
  const mime = (response.headers.get('Content-Type') || '').split(';')[0].trim().toLowerCase();
  if (mime === 'application/wasm' && typeof WebAssembly.instantiateStreaming === 'function') {
    // Compile while downloading. Compile/link failures are real failures;
    // retrying the same module would only repeat the download and work.
    return (await WebAssembly.instantiateStreaming(response, imports)).instance;
  }
  // Reuse the fetched response for hosts with a missing/wrong MIME type.
  // No second request or cloned response retaining the whole download.
  return (await WebAssembly.instantiate(await response.arrayBuffer(), imports)).instance;
}

function initWasmWorker(options = {}) {
  if (options.protocolVersion !== undefined && options.protocolVersion !== 1 && options.protocolVersion !== PROTOCOL_VERSION) {
    return Promise.reject(new Error('Unsupported nanoGo client protocol version: ' + options.protocolVersion));
  }
  // Concurrent init messages share both the download and the Go runtime.
  // A failed partially-started runtime must be replaced with a new worker.
  if (_runtimeFailure) return Promise.reject(_runtimeFailure);
  if (!_initPromise) _initPromise = startWasmWorker(options);
  return _initPromise;
}

async function startWasmWorker(options) {
  const supplied = Number(options.wasmBytes !== undefined) + Number(options.wasmModule !== undefined);
  if (supplied > 1 || (supplied && options.wasmURL !== undefined)) {
    throw new TypeError('Choose exactly one of wasmBytes, wasmModule, or wasmURL');
  }
  if (options.offline && (!supplied || options.wasmExecProvided !== true || options.wasmExecURL !== undefined)) {
    throw new TypeError('offline mode requires supplied WASM and runtime');
  }
  if (options.wasmExecProvided === true) {
    if (options.wasmExecURL !== undefined) throw new TypeError('Choose supplied runtime or wasmExecURL');
    if (typeof Go !== 'function') throw new Error('Supplied Go runtime is unavailable');
  } else {
    importScripts(options.wasmExecURL || 'wasm_exec.js');
  }
  const go = new Go();

  // Hook for Go to signal readiness once it has registered its globals.
  // Replaces the previous polling loop.
  self.nanoGoSignalReady = function () {
    if (_readyResolve) { _readyResolve(); _readyResolve = null; }
  };
  // Hook for runtime to call to send structured messages to host.
  // Routed through the batching layer above.
  self.nanoGoPostMessage = postFromGuest;
  self.nanoGoPostConsole = postConsoleFromGuest;

  // Bump this whenever nanogo.wasm is rebuilt. Unlike the other assets here,
  // the .wasm binary was fetched by plain URL with no cache-busting query at
  // all — on a browser that never installs (or hasn't yet activated) the
  // service worker, the plain HTTP cache could keep serving a stale
  // interpreter build indefinitely after a deploy.
  const WASM_URL = options.wasmURL || 'nanogo.wasm?8';

  // Prefer streaming instantiation: starts compilation while bytes are
  // still arriving and avoids buffering the full module in memory.
  let instance;
  if (options.wasmModule !== undefined) {
    if (!(options.wasmModule instanceof WebAssembly.Module)) throw new TypeError('wasmModule must be a compiled WebAssembly.Module');
    instance = await WebAssembly.instantiate(options.wasmModule, go.importObject);
  } else if (options.wasmBytes !== undefined) {
    if (!(options.wasmBytes instanceof ArrayBuffer) && !ArrayBuffer.isView(options.wasmBytes)) {
      throw new TypeError('wasmBytes must be an ArrayBuffer or ArrayBuffer view');
    }
    instance = (await WebAssembly.instantiate(options.wasmBytes, go.importObject)).instance;
  } else {
    instance = await instantiateWasm(WASM_URL, go.importObject);
  }

  // Run the Go program (this registers nanoGo* globals via syscall/js).
  const exited = Promise.resolve(go.run(instance)).then(() => {
    throw new Error('Go runtime exited');
  });
  // Observe later exits too, after startup has already completed. Without
  // this handler Go failures become unhandled rejections and clients wait
  // forever for replies from a runtime that no longer exists.
  exited.catch((error) => {
    _runtimeFailure = error;
    if (!goReady) return;
    goReady = false;
    self.postMessage({ type: 'error', fatal: true, text: String(error) });
  });

  // If the Go side calls nanoGoSignalReady the promise is resolved
  // synchronously during go.run. If the build doesn't include that
  // hook (older WASM), fall back to a short bounded poll so old
  // builds still work — but we only spin for a short window.
  let pollTimer;
  let readyTimer;
  try {
    await Promise.race([
      _readyPromise,
      exited,
      new Promise((resolve, reject) => {
        const poll = () => {
          if (typeof self.nanoGoRun === 'function') resolve();
          else pollTimer = setTimeout(poll, 10);
        };
        readyTimer = setTimeout(() => reject(new Error('nanoGoRun not registered')), 3000);
        poll();
      })
    ]);
  } finally {
    clearTimeout(pollTimer);
    clearTimeout(readyTimer);
  }
  if (_runtimeFailure) throw _runtimeFailure;

  // Make sure references are populated (Go usually puts them on globalThis).
  self.nanoGoRun      = self.nanoGoRun      || self.globalThis?.nanoGoRun;
  self.nanoGoFormat   = self.nanoGoFormat   || self.globalThis?.nanoGoFormat;
  self.nanoGoVet      = self.nanoGoVet      || self.globalThis?.nanoGoVet;
  self.nanoGoAst        = self.nanoGoAst        || self.globalThis?.nanoGoAst;
  self.nanoGoCallGraph  = self.nanoGoCallGraph  || self.globalThis?.nanoGoCallGraph;
  self.nanoGoBench      = self.nanoGoBench      || self.globalThis?.nanoGoBench;
  self.nanoGoVersion  = self.nanoGoVersion  || self.globalThis?.nanoGoVersion;
  self.nanoGoRunWorkspace = self.nanoGoRunWorkspace || self.globalThis?.nanoGoRunWorkspace;
  self.nanoGoWorkspaceCheck = self.nanoGoWorkspaceCheck || self.globalThis?.nanoGoWorkspaceCheck;
  self.nanoGoTestWorkspace = self.nanoGoTestWorkspace || self.globalThis?.nanoGoTestWorkspace;

  if (typeof self.nanoGoRun !== 'function') {
    throw new Error('nanoGoRun not registered');
  }
  goReady = true;
}
