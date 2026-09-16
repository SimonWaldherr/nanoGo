// Dependency-free browser client. Serve this module next to wasm_worker.js,
// wasm_exec.js and nanogo.wasm, or configure their URLs in createNanoGo().

export const PROTOCOL_VERSION = 2;

function clientError(message, code, phase = 'host') {
  const error = new Error(message);
  error.diagnostic = {code, phase, message};
  return error;
}

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
    this.protocolVersion = 1;
    this._requiresSuppliedAssets = options.offline || options.wasmBytes !== undefined ||
      options.wasmModule !== undefined || options.wasmExecProvided;
    const created = options.workerFactory ? options.workerFactory() :
      new Worker(options.workerURL || new URL('./wasm_worker.js', import.meta.url));
    const handle = created && created.worker ? created : {worker: created};
    this._worker = handle.worker;
    this._cleanup = typeof handle.dispose === 'function' ? () => handle.dispose() : null;
    const wasmExecProvided = options.wasmExecProvided === true || handle.wasmExecProvided === true;
    this.ready = new Promise((resolve, reject) => {
      this._resolveReady = resolve;
      this._rejectReady = reject;
    });
    if (!this._worker || typeof this._worker.postMessage !== 'function' ||
        typeof this._worker.terminate !== 'function') {
      this._fail(new TypeError('workerFactory must return a dedicated Worker or worker handle'));
      return;
    }
    if (options.offline && !wasmExecProvided) {
      this._fail(new TypeError('offline mode requires a supplied Go runtime'));
      return;
    }
    this._timer = setTimeout(() => {
      this._fail(clientError('nanoGo initialization timed out', 'initialization.deadline', 'cancel'));
    }, options.initTimeoutMs ?? 30000);
    this._worker.onmessage = ({ data }) => this._receive(data);
    this._worker.onerror = (event) => this._fail(new Error(event.message || 'nanoGo worker failed'));
    this._worker.onmessageerror = () => this._fail(new Error('nanoGo worker response could not be decoded'));
    try {
      this._worker.postMessage({ type: 'init', protocolVersion: PROTOCOL_VERSION,
        wasmURL: options.wasmURL, wasmExecURL: options.wasmExecURL,
        ...(options.wasmBytes !== undefined ? {wasmBytes: options.wasmBytes} : {}),
        ...(options.wasmModule !== undefined ? {wasmModule: options.wasmModule} : {}),
        ...(wasmExecProvided ? {wasmExecProvided: true} : {}),
        ...(options.offline ? {offline: true} : {}) });
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
      const version = message.protocolVersion ?? 1;
      if ((version !== 1 && version !== PROTOCOL_VERSION) || (this._requiresSuppliedAssets && version !== PROTOCOL_VERSION)) {
        this._fail(clientError('Unsupported nanoGo worker protocol version: ' + version, 'host.protocol'));
        return;
      }
      this.protocolVersion = version;
      this.capabilities = message.capabilities || {};
      this._ready = true;
      clearTimeout(this._timer);
      this._resolveReady(this);
      this._drain();
    } else if (message.type === 'error' && (!this._ready || message.fatal)) {
      const error = clientError(message.text || 'nanoGo initialization failed', 'host.worker');
      if (message.diagnostic) error.diagnostic = message.diagnostic;
      error.response = message;
      this._fail(error);
    } else if (this._active && message.type === replies[this._active.type]) {
      const active = this._active;
      clearTimeout(active.timer);
      this._active = null;
      const detail = message.error || (message.stats && message.stats.error);
      if (detail) {
        const error = new Error(detail);
        error.response = message;
        error.diagnostic = message.diagnostic || message.stats?.diagnostic || message.result?.diagnostic;
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
        this._fail(clientError('nanoGo request timed out', 'execution.deadline', 'cancel'));
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
    if ((type === 'run' || type === 'workspace-run') &&
        (payload.inputs !== undefined || payload.limits !== undefined) &&
        (!this.capabilities.hasStructuredResults || this.protocolVersion < 2)) {
      return Promise.reject(new Error('This nanoGo worker/WASM combination does not support inputs and limits'));
    }
    return new Promise((resolve, reject) => {
      this._queue.push({ type, payload, resolve, reject });
      this._drain();
    });
  }

  _fail(error) {
    if (this._closed) return;
    if (!(error instanceof Error)) error = new Error(String(error));
    if (!error.diagnostic) error.diagnostic = {code:'host.worker',phase:'host',message:error.message || String(error)};
    this._closed = error;
    clearTimeout(this._timer);
    try { this._worker?.terminate?.(); } catch { /* still reject and release assets */ }
    try { this._cleanup?.(); } catch { /* cleanup cannot strand pending requests */ }
    this._cleanup = null;
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
  dispose() { this._fail(clientError('nanoGo client disposed', 'execution.disposed', 'cancel')); }
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
  const supplied = Number(options.wasmBytes !== undefined) + Number(options.wasmModule !== undefined);
  if (supplied > 1 || (supplied && options.wasmURL !== undefined)) {
    throw new TypeError('Choose exactly one of wasmBytes, wasmModule, or wasmURL');
  }
  if (options.workerFactory !== undefined && (typeof options.workerFactory !== 'function' || options.workerURL !== undefined)) {
    throw new TypeError('Choose workerFactory or workerURL');
  }
  if (options.wasmExecProvided && options.wasmExecURL !== undefined) {
    throw new TypeError('Choose supplied runtime or wasmExecURL');
  }
  if (options.offline && (!supplied || !options.workerFactory || options.wasmExecURL !== undefined)) {
    throw new TypeError('offline mode requires supplied WASM, runtime, and a workerFactory');
  }
  if (options.wasmModule !== undefined && !(options.wasmModule instanceof WebAssembly.Module)) {
    throw new TypeError('wasmModule must be a compiled WebAssembly.Module');
  }
  if (options.wasmBytes !== undefined) {
    const bytes = options.wasmBytes;
    if (!(bytes instanceof ArrayBuffer) && !ArrayBuffer.isView(bytes)) {
      throw new TypeError('wasmBytes must be an ArrayBuffer or ArrayBuffer view');
    }
    if (ArrayBuffer.isView(bytes) && !(bytes.buffer instanceof ArrayBuffer)) {
      throw new TypeError('wasmBytes must not use shared memory');
    }
    // Snapshot only the selected view. Never transfer/detach host-owned data.
    options = {...options, wasmBytes: bytes instanceof ArrayBuffer ? bytes.slice(0) :
      bytes.buffer.slice(bytes.byteOffset, bytes.byteOffset + bytes.byteLength)};
  }
  const client = new NanoGoClient(options);
  return client.ready;
}

/** Build a dedicated classic worker from trusted source, without eval, URLs,
 * imports, or fetch. The matching Go runtime executes before the worker SDK.
 * Each invocation owns a new worker and blob URL until client disposal. */
export function createInlineWorkerFactory({workerSource, wasmExecSource}) {
  if (typeof workerSource !== 'string' || !workerSource.trim() ||
      typeof wasmExecSource !== 'string' || !wasmExecSource.trim()) {
    throw new TypeError('workerSource and wasmExecSource must be nonempty source strings');
  }
  return () => {
    const url = URL.createObjectURL(new Blob([wasmExecSource, '\n;\n', workerSource], {type:'text/javascript'}));
    try {
      const worker = new Worker(url);
      return {worker, wasmExecProvided:true, dispose:() => URL.revokeObjectURL(url)};
    } catch (error) {
      URL.revokeObjectURL(url);
      throw error;
    }
  };
}
