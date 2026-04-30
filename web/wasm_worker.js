// wasm_worker.js
// Runs the Go WASM in a Web Worker and forwards structured messages to the main thread.

let goReady = false;
// Resolved by Go (via the `nanoGoSignalReady` host helper) once all
// `nanoGo*` globals have been registered. This avoids the previous
// busy-wait loop that polled every 10 ms for up to 3 seconds.
let _readyResolve;
const _readyPromise = new Promise((resolve) => { _readyResolve = resolve; });

self.onmessage = async function (ev) {
  const msg = ev.data;
  if (msg && msg.type === 'init') {
    try {
      await initWasmWorker();
      self.postMessage({ type: 'ready' });
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
      if (msg.mode === 'deferred') {
        // Buffer all worker->host messages until the run completes,
        // then flush them in order. Wrap in try/finally so a thrown
        // error in user code can't leave postMessage permanently
        // replaced (which would silently swallow future messages).
        const origPost = self.postMessage;
        const buffer = [];
        self.postMessage = function (m) { buffer.push(m); };
        try {
          self.nanoGoRun(msg.source);
        } finally {
          self.postMessage = origPost;
          for (const m of buffer) { origPost.call(self, m); }
          origPost.call(self, { type: 'done', elapsed: Date.now() - t0 });
        }
      } else {
        self.nanoGoRun(msg.source);
        self.postMessage({ type: 'done', elapsed: Date.now() - t0 });
      }
    } catch (err) {
      self.postMessage({ type: 'error', text: String(err) });
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
  self.nanoGoPostMessage = function (msg) { self.postMessage(msg); };

  // Prefer streaming instantiation: starts compilation while bytes are
  // still arriving and avoids buffering the full module in memory.
  let instance;
  try {
    if (typeof WebAssembly.instantiateStreaming === 'function') {
      const result = await WebAssembly.instantiateStreaming(fetch('nanogo.wasm'), go.importObject);
      instance = result.instance;
    } else {
      throw new Error('instantiateStreaming unavailable');
    }
  } catch (e) {
    // Fallback path for servers that don't serve application/wasm with
    // the correct MIME type, or for older runtimes.
    const resp = await fetch('nanogo.wasm');
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
  self.nanoGoRun     = self.nanoGoRun     || self.globalThis?.nanoGoRun;
  self.nanoGoSetScale= self.nanoGoSetScale|| self.globalThis?.nanoGoSetScale;
  self.nanoGoFormat  = self.nanoGoFormat  || self.globalThis?.nanoGoFormat;
  self.nanoGoVet     = self.nanoGoVet     || self.globalThis?.nanoGoVet;
  self.nanoGoVersion = self.nanoGoVersion || self.globalThis?.nanoGoVersion;

  if (typeof self.nanoGoRun !== 'function') {
    throw new Error('nanoGoRun not registered');
  }
  goReady = true;
}
