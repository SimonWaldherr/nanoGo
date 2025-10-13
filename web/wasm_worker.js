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
    try {
      if (msg.mode === 'deferred') {
        self.deferred = true;
        self.deferredLog = [];
        self._origPost = self.postMessage;
        self.postMessage = function (m) { self.deferredLog.push(m); };
        self.nanoGoRun(msg.source);
        self.postMessage = self._origPost;
        for (const m of self.deferredLog) { self._origPost(m); }
        self._origPost({ type: 'done' });
      } else {
        self.nanoGoRun(msg.source);
        self.postMessage({ type: 'done' });
      }
    } catch (err) {
      self.postMessage({ type: 'error', text: String(err) });
    }
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

  // Wait until nanoGoRun is actually available
  const deadline = Date.now() + 3000;
  while (typeof self.nanoGoRun !== 'function') {
    if (Date.now() > deadline) throw new Error('nanoGoRun not registered');
    await new Promise(r => setTimeout(r, 10));
    self.nanoGoRun = self.globalThis?.nanoGoRun;
  }
  goReady = true;
}
