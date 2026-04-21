// wasm_worker.js
// Runs the Go WASM in a Web Worker and forwards structured messages to the main thread.

let goReady = false;

self.onmessage = async function (ev) {
  const msg = ev.data;
  if (msg && msg.type === 'init') {
    await initWasmWorker();
    self.postMessage({ type: 'ready' });
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
        self.deferred = true;
        self.deferredLog = [];
        self._origPost = self.postMessage;
        self.postMessage = function (m) { self.deferredLog.push(m); };
        self.nanoGoRun(msg.source);
        self.postMessage = self._origPost;
        for (const m of self.deferredLog) { self._origPost(m); }
        self._origPost({ type: 'done', elapsed: Date.now() - t0 });
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
  const resp = await fetch('nanogo.wasm');
  const buf = await resp.arrayBuffer();
  const result = await WebAssembly.instantiate(buf, go.importObject);
  // Hook for runtime to call to send structured messages to host
  self.nanoGoPostMessage = function (msg) { self.postMessage(msg); };
  // Run the Go program (this will register nanoGoRun globally)
  go.run(result.instance);

  // Expose helpers that Go registered on the global scope via syscall/js
  self.nanoGoRun = self.nanoGoRun || self.globalThis?.nanoGoRun || self.nanoGoRun;
  self.nanoGoSetScale = self.nanoGoSetScale || self.globalThis?.nanoGoSetScale || self.nanoGoSetScale;
  self.nanoGoFormat = self.nanoGoFormat || self.globalThis?.nanoGoFormat;
  self.nanoGoVet = self.nanoGoVet || self.globalThis?.nanoGoVet;
  self.nanoGoVersion = self.nanoGoVersion || self.globalThis?.nanoGoVersion;

  // Wait until nanoGoRun is actually available
  const deadline = Date.now() + 3000;
  while (typeof self.nanoGoRun !== 'function') {
    if (Date.now() > deadline) throw new Error('nanoGoRun not registered');
    await new Promise(r => setTimeout(r, 10));
    self.nanoGoRun = self.globalThis?.nanoGoRun;
    self.nanoGoFormat = self.globalThis?.nanoGoFormat;
    self.nanoGoVet = self.globalThis?.nanoGoVet;
    self.nanoGoVersion = self.globalThis?.nanoGoVersion;
  }
  goReady = true;
}
