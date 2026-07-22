// wasm_worker.js
// Runs the Go WASM in a Web Worker and forwards structured messages to the main thread.

let goReady = false;
// Resolved by Go (via the `nanoGoSignalReady` host helper) once all
// `nanoGo*` globals have been registered. This avoids the previous
// busy-wait loop that polled every 10 ms for up to 3 seconds.
let _readyResolve;
const _readyPromise = new Promise((resolve) => { _readyResolve = resolve; });

// ---------------------------------------------------------------------------
// Outbound message batching.
//
// Guest programs can emit thousands of `log` / `canvas-set` messages in a
// tight loop (Game of Life paints every cell of every generation). One
// postMessage per event means one structured clone + one main-thread task
// per cell, which dominates total run time. High-frequency message types are
// therefore buffered and shipped as a single `{type:'batch', items:[...]}`
// message: on flush points (canvas-flush, run end), when the buffer is full,
// or on the next microtask once the synchronous Go slice yields (covers
// animated demos that time.Sleep between frames).
// ---------------------------------------------------------------------------
const BATCHABLE = { 'log': 1, 'warn': 1, 'canvas-set': 1, 'canvas-set-level': 1, 'canvas-size': 1, 'canvas-flush': 1, 'canvas-frame': 1 };
const BATCH_LIMIT = 2048;
let _batch = [];
let _batchScheduled = false;

function flushBatch() {
  _batchScheduled = false;
  if (_batch.length === 0) return;
  const items = _batch;
  _batch = [];
  if (items.length === 1) {
    self.postMessage(items[0]);
  } else {
    self.postMessage({ type: 'batch', items: items });
  }
}

function postFromGuest(msg) {
  const t = msg && msg.type;
  if (t && BATCHABLE[t]) {
    _batch.push(msg);
    // canvas-frame is an explicit guest flush. Deliver it immediately so an
    // animation's frame order survives even when Go yields faster than the
    // browser's next paint.
    if (t === 'canvas-flush' || t === 'canvas-frame' || _batch.length >= BATCH_LIMIT) {
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
  self.postMessage(msg);
}

// parseStats parses the JSON string a stats-aware WASM build returns from
// nanoGoRun/nanoGoAst/nanoGoBench. Older builds return undefined -> null.
function parseStats(raw) {
  if (typeof raw !== 'string' || raw.length === 0) return null;
  try { return JSON.parse(raw); } catch (e) { return null; }
}

self.onmessage = async function (ev) {
  const msg = ev.data;
  if (msg && msg.type === 'init') {
    try {
      await initWasmWorker();
      self.postMessage({ type: 'ready', capabilities: getCapabilities() });
    } catch (e) {
      self.postMessage({ type: 'error', text: 'WASM init failed: ' + (e && e.message ? e.message : String(e)) });
    }
    return;
  }
  if (msg && msg.type === 'set-scale') {
    try {
      if (typeof self.nanoGoSetScale === 'function') {
        self.nanoGoSetScale(Number(msg.scale|0));
      }
    } catch (e) {/*no-op*/}
    return;
  }
  if (msg && msg.type === 'run') {
    if (!goReady) {
      self.postMessage({ type: 'error', text: 'WASM not initialized' });
      return;
    }
    const t0 = Date.now();
    try {
      let stats = null;
      if (msg.mode === 'deferred') {
        // Buffer all worker->host messages until the run completes,
        // then flush them in order. Wrap in try/finally so a thrown
        // error in user code can't leave postMessage permanently
        // replaced (which would silently swallow future messages).
        const origPost = self.postMessage;
        const buffer = [];
        self.postMessage = function (m) { buffer.push(m); };
        try {
          stats = parseStats(self.nanoGoRun(msg.source, !!msg.trace, !!msg.profile));
        } finally {
          flushBatch();
          self.postMessage = origPost;
          for (const m of buffer) { origPost.call(self, m); }
          origPost.call(self, { type: 'done', elapsed: Date.now() - t0, stats: stats });
        }
      } else {
        stats = parseStats(self.nanoGoRun(msg.source, !!msg.trace, !!msg.profile));
        flushBatch();
        self.postMessage({ type: 'done', elapsed: Date.now() - t0, stats: stats });
      }
    } catch (err) {
      flushBatch();
      self.postMessage({ type: 'error', text: String(err) });
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
        self.postMessage({ type: 'ast-result', error: res.error });
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
        self.postMessage({ type: 'callgraph-result', error: res.error });
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
        self.postMessage({ type: 'format-result', error: result.error });
      } else {
        self.postMessage({ type: 'format-result', source: result && result.source != null ? result.source : msg.source });
      }
    } catch (e) {
      self.postMessage({ type: 'format-result', error: String(e) });
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
        self.postMessage({ type: 'vet-result', error: raw.error });
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
    hasAst: typeof self.nanoGoAst === 'function',
    hasCallGraph: typeof self.nanoGoCallGraph === 'function',
    hasBench: typeof self.nanoGoBench === 'function'
  };
}

async function initWasmWorker() {
  if (goReady) return;
  importScripts('wasm_exec.js');
  const go = new Go();

  // Hook for Go to signal readiness once it has registered its globals.
  // Replaces the previous polling loop.
  self.nanoGoSignalReady = function () {
    if (_readyResolve) { _readyResolve(); _readyResolve = null; }
  };
  // Hook for runtime to call to send structured messages to host.
  // Routed through the batching layer above.
  self.nanoGoPostMessage = postFromGuest;

  // Bump this whenever nanogo.wasm is rebuilt. Unlike the other assets here,
  // the .wasm binary was fetched by plain URL with no cache-busting query at
  // all — on a browser that never installs (or hasn't yet activated) the
  // service worker, the plain HTTP cache could keep serving a stale
  // interpreter build indefinitely after a deploy.
  const WASM_URL = 'nanogo.wasm?7';

  // Prefer streaming instantiation: starts compilation while bytes are
  // still arriving and avoids buffering the full module in memory.
  let instance;
  try {
    if (typeof WebAssembly.instantiateStreaming === 'function') {
      const result = await WebAssembly.instantiateStreaming(fetch(WASM_URL), go.importObject);
      instance = result.instance;
    } else {
      throw new Error('instantiateStreaming unavailable');
    }
  } catch (e) {
    // Fallback path for servers that don't serve application/wasm with
    // the correct MIME type, or for older runtimes.
    const resp = await fetch(WASM_URL);
    const buf = await resp.arrayBuffer();
    const result = await WebAssembly.instantiate(buf, go.importObject);
    instance = result.instance;
  }

  // Run the Go program (this registers nanoGo* globals via syscall/js).
  go.run(instance);

  // If the Go side calls nanoGoSignalReady the promise is resolved
  // synchronously during go.run. If the build doesn't include that
  // hook (older WASM), fall back to a short bounded poll so old
  // builds still work — but we only spin for a short window.
  await Promise.race([
    _readyPromise,
    (async () => {
      const deadline = Date.now() + 3000;
      while (typeof self.nanoGoRun !== 'function') {
        if (Date.now() > deadline) throw new Error('nanoGoRun not registered');
        await new Promise(r => setTimeout(r, 10));
      }
    })()
  ]);

  // Make sure references are populated (Go usually puts them on globalThis).
  self.nanoGoRun      = self.nanoGoRun      || self.globalThis?.nanoGoRun;
  self.nanoGoSetScale = self.nanoGoSetScale || self.globalThis?.nanoGoSetScale;
  self.nanoGoFormat   = self.nanoGoFormat   || self.globalThis?.nanoGoFormat;
  self.nanoGoVet      = self.nanoGoVet      || self.globalThis?.nanoGoVet;
  self.nanoGoAst        = self.nanoGoAst        || self.globalThis?.nanoGoAst;
  self.nanoGoCallGraph  = self.nanoGoCallGraph  || self.globalThis?.nanoGoCallGraph;
  self.nanoGoBench      = self.nanoGoBench      || self.globalThis?.nanoGoBench;
  self.nanoGoVersion  = self.nanoGoVersion  || self.globalThis?.nanoGoVersion;

  if (typeof self.nanoGoRun !== 'function') {
    throw new Error('nanoGoRun not registered');
  }
  goReady = true;
}
