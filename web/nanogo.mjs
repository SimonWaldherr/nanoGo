// Dependency-free browser client. Serve this module next to wasm_worker.js,
// wasm_exec.js and nanogo.wasm, or configure their URLs in createNanoGo().

const replies = {
  run: 'done', format: 'format-result', vet: 'vet-result', test: 'test-result',
  'workspace-check': 'workspace-check-result',
  'workspace-run': 'workspace-done', 'workspace-test': 'workspace-test-result'
};

class NanoGoClient {
  constructor(options) {
    this.capabilities = {};
    this._queue = [];
    this._active = null;
    this._closed = null;
    this._ready = false;
    this._onMessage = options.onMessage;
    this._requestTimeoutMs = options.requestTimeoutMs ?? 0;
    this._worker = new Worker(options.workerURL || new URL('./wasm_worker.js', import.meta.url));
    this.ready = new Promise((resolve, reject) => {
      this._resolveReady = resolve;
      this._rejectReady = reject;
    });
    this._timer = setTimeout(() => {
      this._fail(new Error('nanoGo initialization timed out'));
    }, options.initTimeoutMs ?? 30000);
    this._worker.onmessage = ({ data }) => this._receive(data);
    this._worker.onerror = (event) => this._fail(new Error(event.message || 'nanoGo worker failed'));
    this._worker.onmessageerror = () => this._fail(new Error('nanoGo worker response could not be decoded'));
    try {
      this._worker.postMessage({ type: 'init', wasmURL: options.wasmURL, wasmExecURL: options.wasmExecURL });
    } catch (error) {
      this._fail(error);
    }
  }

  _receive(message) {
    if (this._closed || !message || typeof message.type !== 'string') return;
    if (message.type === 'batch') {
      if (Array.isArray(message.items)) {
        for (const item of message.items) this._receive(item);
      }
      return;
    }
    if (message.type === 'ready') {
      this.capabilities = message.capabilities || {};
      this._ready = true;
      clearTimeout(this._timer);
      this._resolveReady(this);
      this._drain();
    } else if (message.type === 'error' && (!this._ready || message.fatal)) {
      this._fail(new Error(message.text || 'nanoGo initialization failed'));
    } else if (this._active && message.type === replies[this._active.type]) {
      const active = this._active;
      clearTimeout(active.timer);
      this._active = null;
      const detail = message.error || (message.stats && message.stats.error);
      if (detail) {
        const error = new Error(detail);
        error.response = message;
        active.reject(error);
      } else {
        active.resolve(message);
      }
      this._drain();
    }
    if (this._onMessage) {
      try {
        this._onMessage(message);
      } catch (error) {
        // Application rendering errors must not strand the request queue or
        // discard the remaining messages in a worker output batch.
        if (typeof globalThis.reportError === 'function') globalThis.reportError(error);
        else console.error(error);
      }
    }
  }

  _drain() {
    if (!this._ready || this._closed || this._active || !this._queue.length) return;
    this._active = this._queue.shift();
    if (this._requestTimeoutMs > 0) {
      this._active.timer = setTimeout(() => {
        // Older workers can emit an error without a terminal response.
        // Terminate before rejecting the queue: a late response has no ID
        // and must never be mistaken for the next operation's result.
        this._fail(new Error('nanoGo request timed out'));
      }, this._requestTimeoutMs);
    }
    try {
      this._worker.postMessage({ ...this._active.payload, type: this._active.type });
    } catch (error) {
      clearTimeout(this._active.timer);
      this._active.reject(error);
      this._active = null;
      this._drain();
    }
  }

  _request(type, payload) {
    if (this._closed) return Promise.reject(this._closed);
    return new Promise((resolve, reject) => {
      this._queue.push({ type, payload, resolve, reject });
      this._drain();
    });
  }

  _fail(error) {
    if (this._closed) return;
    this._closed = error;
    clearTimeout(this._timer);
    this._worker.terminate();
    this._rejectReady(error);
    if (this._active) {
      clearTimeout(this._active.timer);
      this._active.reject(error);
    }
    this._active = null;
    for (const request of this._queue) request.reject(error);
    this._queue = [];
  }

  run(source, options = {}) { return this._request('run', { ...options, source }); }
  format(source) { return this._request('format', { source }); }
  vet(source) { return this._request('vet', { source }); }
  test(source, options = {}) { return this._request('test', { ...options, source }); }
  checkWorkspace(files, options = {}) { return this._request('workspace-check', { ...options, files }); }
  runWorkspace(files, options = {}) { return this._request('workspace-run', { ...options, files }); }
  testWorkspace(files, options = {}) { return this._request('workspace-test', { ...options, files }); }

  // Termination also stops non-terminating guest programs. Create a new
  // client to resume; the new worker starts with fresh guest/storage state.
  dispose() { this._fail(new Error('nanoGo client disposed')); }
}

/** Create one ready worker. All operations are serialized and return the
 * full terminal protocol message. Errors carry that message in .response.
 * onMessage receives unwrapped output in order; it never applies DOM effects.
 */
export async function createNanoGo(options = {}) {
  if (options.requestTimeoutMs !== undefined &&
      (!Number.isFinite(options.requestTimeoutMs) || options.requestTimeoutMs < 0)) {
    throw new TypeError('requestTimeoutMs must be a nonnegative finite number');
  }
  if (options.initTimeoutMs !== undefined &&
      (!Number.isFinite(options.initTimeoutMs) || options.initTimeoutMs <= 0)) {
    throw new TypeError('initTimeoutMs must be a positive finite number');
  }
  const client = new NanoGoClient(options);
  return client.ready;
}
