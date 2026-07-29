/* global window */
//
// app.js — a minimal, from-scratch reference frontend for nanoGo's WASM
// worker, independent of index.html's full CodeMirror-based playground.
//
// This is NOT loaded by index.html (that page owns its UI directly in an
// inline <script>). It exists for third parties who want to build their own
// frontend against cmd/wasm's WASM build and need a short, readable example
// of the worker message protocol rather than reverse-engineering it out of
// the ~2000-line production UI. Pair it with web/minimal.html, which
// supplies the plain <textarea>/<button> markup this file expects, and
// reuses this same directory's already-built nanogo.wasm, wasm_exec.js and
// wasm_worker.js — no separate build step.
//
// See the README's "Embedding" section for the full protocol reference.
(function () {
  'use strict';

  const statusEl = document.getElementById('status');
  const logEl = document.getElementById('log');
  const srcEl = document.getElementById('src');
  const runBtn = document.getElementById('runBtn');
  const stopBtn = document.getElementById('stopBtn');
  const formatBtn = document.getElementById('formatBtn');
  const vetBtn = document.getElementById('vetBtn');
  const testBtn = document.getElementById('testBtn');
  const testFilterEl = document.getElementById('testFilter');
  const clearBtn = document.getElementById('clearBtn');
  const shareBtn = document.getElementById('shareBtn');
  const scaleEl = document.getElementById('scale');
  const exampleSelect = document.getElementById('exampleSelect');
  const canvasEl = document.getElementById('life');

  // A handful of self-contained snippets, kept inline rather than depending
  // on web/examples.js's specific format — a real integration would swap
  // this for its own content.
  const EXAMPLES = {
    'Hello World': `package main

import "fmt"

func main() {
  fmt.Println("Hello from a minimal nanoGo frontend!")
}
`,
    'Canvas': `package main

import "browser"

func main() {
  browser.CanvasSize(8, 8)
  for y := 0; y < 8; y++ {
    for x := 0; x < 8; x++ {
      browser.CanvasSet(x, y, (x+y)%2 == 0)
    }
  }
  browser.CanvasFlush()
}
`,
    'Channels': `package main

import "fmt"

func main() {
  ch := make(chan int, 3)
  for i := 1; i <= 3; i++ { ch <- i * i }
  close(ch)
  for v := range ch {
    fmt.Println("squared:", v)
  }
}
`
  };

  function setStatus(message, type) {
    if (!statusEl) return;
    statusEl.textContent = message;
    statusEl.className = 'status ' + (type || 'loading');
  }

  function log(message) {
    if (!logEl) return;
    logEl.textContent += message + '\n';
    logEl.scrollTop = logEl.scrollHeight;
  }

  function drawCell(x, y, alive) {
    if (!canvasEl) return;
    const ctx = canvasEl.getContext('2d');
    const cellSize = parseInt((scaleEl && scaleEl.value) || '10', 10) || 10;
    if (alive) ctx.fillRect(x * cellSize, y * cellSize, cellSize, cellSize);
    else ctx.clearRect(x * cellSize, y * cellSize, cellSize, cellSize);
  }

  let worker = null;

  function startWorker() {
    if (worker) return;
    worker = new Worker('wasm_worker.js');

    // The worker coalesces high-frequency messages (console lines, canvas
    // updates) into `{type:'batch', items:[...]}` so a tight guest loop
    // doesn't cost one postMessage per line/pixel. Unwrap it through the
    // same dispatcher as an ordinary message, or a program like the "Life"
    // demo in the full playground will appear to produce almost no output.
    worker.onmessage = (ev) => {
      const msg = ev.data;
      if (!msg || !msg.type) return;
      if (msg.type === 'batch') {
        for (const item of msg.items) handleMessage(item);
        return;
      }
      handleMessage(msg);
    };
    worker.onerror = (err) => {
      setStatus('Worker error', 'error');
      log('worker error: ' + err.message);
    };
    worker.postMessage({ type: 'init' });
  }

  function handleMessage(m) {
    switch (m.type) {
      case 'ready':
        setStatus('Ready', 'ready');
        if (runBtn) runBtn.disabled = false;
        log('WASM ready. Capabilities: ' + JSON.stringify(m.capabilities || {}));
        loadCodeFromURL();
        break;
      case 'log':
        log(String(m.text));
        break;
      case 'warn':
        log('WARN: ' + String(m.text));
        break;
      case 'error':
        log('ERROR: ' + String(m.text));
        setStatus('Runtime error', 'error');
        break;
      case 'canvas-size': {
        const w = Number(m.w), h = Number(m.h);
        const cellSize = parseInt((scaleEl && scaleEl.value) || '10', 10) || 10;
        if (canvasEl) { canvasEl.width = w * cellSize; canvasEl.height = h * cellSize; }
        break;
      }
      case 'canvas-set':
        drawCell(Number(m.x), Number(m.y), !!m.alive);
        break;
      case 'canvas-set-level':
        // A minimal frontend can treat any non-zero palette level as "on";
        // the full playground's canvas panel (see index.html) instead maps
        // each level 0-7 to a distinct color.
        drawCell(Number(m.x), Number(m.y), Number(m.level) > 0);
        break;
      case 'canvas-frame':
        // A whole animation frame encoded as "x,y,level;x,y,level;...".
        // Decoding it in full (as index.html does, for performance) is out
        // of scope for this minimal example — draw each cell directly.
        String(m.data || '').split(';').filter(Boolean).forEach(cell => {
          const [x, y, level] = cell.split(',').map(Number);
          drawCell(x, y, level > 0);
        });
        break;
      case 'canvas-flush':
        break; // nothing to do: drawCell already paints immediately above
      case 'dom-setinner': {
        const el = document.getElementById(m.id);
        if (el) el.innerHTML = m.html;
        break;
      }
      case 'dom-setvalue': {
        const el = document.getElementById(m.id);
        if (el) el.value = m.value;
        break;
      }
      case 'dom-addclass': {
        const el = document.getElementById(m.id);
        if (el) el.classList.add(m.class);
        break;
      }
      case 'dom-removeclass': {
        const el = document.getElementById(m.id);
        if (el) el.classList.remove(m.class);
        break;
      }
      case 'open-window':
        try { window.open(m.url, '_blank'); } catch (e) { /* ignore */ }
        break;
      case 'alert':
        try { window.alert(m.text); } catch (e) { /* ignore */ }
        break;
      case 'format-result':
        if (m.error) log('format error: ' + m.error);
        else if (m.source != null && srcEl) { srcEl.value = m.source; log('code formatted'); }
        break;
      case 'vet-result':
        if (m.error) log('vet error: ' + m.error);
        else log('vet: ' + (m.issues && m.issues.length ? m.issues.length + ' issue(s) — see console' : 'no issues'));
        if (m.issues) m.issues.forEach(iss => console.log('vet:', iss.line + ':' + iss.column, iss.message));
        break;
      case 'test-result':
        if (m.error) log('test error: ' + m.error);
        else if (!m.result || !m.result.total) log('no matching TestXxx functions found');
        else if (m.result.passed) log('PASS: ' + m.result.total + ' test(s) passed' + ((m.result.results || []).some(test => test.skipped) ? ' (includes skipped tests)' : ''));
        else {
          log('FAIL: ' + m.result.failed + ' of ' + m.result.total + ' test(s) failed');
          (m.result.results || []).filter(test => !test.passed).forEach(test => {
            const detail = (test.messages || []).join('; ') || test.category;
            log('  ' + (test.name || 'Test') + ': ' + detail);
          });
        }
        break;
      // ast-result / callgraph-result / bench-result carry structured JSON
      // meant for a real inspector UI (see index.html's Inspector panel for
      // a full implementation) — this minimal example just surfaces that
      // they arrived, via the browser console, rather than rendering one.
      case 'ast-result':
      case 'callgraph-result':
      case 'bench-result':
        console.log(m.type, m);
        log(m.type + ' received (see browser console for the full JSON)');
        break;
      case 'done':
        log('=== finished' + (m.elapsed != null ? ' (' + m.elapsed + 'ms)' : '') + ' ===');
        setStatus('Ready', 'ready');
        break;
      default:
        console.log('worker:', m);
    }
  }

  function runCode() {
    if (!worker) return;
    const scale = parseInt((scaleEl && scaleEl.value) || '10', 10);
    worker.postMessage({ type: 'set-scale', scale: scale });
    if (canvasEl) canvasEl.getContext('2d').clearRect(0, 0, canvasEl.width, canvasEl.height);
    log('=== running ===');
    setStatus('Running…', 'loading');
    worker.postMessage({ type: 'run', source: srcEl.value, mode: 'stream' });
  }
  if (runBtn) runBtn.onclick = runCode;

  if (stopBtn) stopBtn.onclick = () => {
    if (!worker) return;
    worker.terminate();
    worker = null;
    setStatus('Stopped', 'error');
    log('=== stopped ===');
    startWorker();
  };

  if (formatBtn) formatBtn.onclick = () => {
    if (worker) worker.postMessage({ type: 'format', source: srcEl.value });
  };

  if (vetBtn) vetBtn.onclick = () => {
    if (worker) worker.postMessage({ type: 'vet', source: srcEl.value });
  };

  if (testBtn) testBtn.onclick = () => {
    if (worker) worker.postMessage({ type: 'test', source: srcEl.value, filter: testFilterEl ? testFilterEl.value.trim() : '' });
  };

  if (clearBtn) clearBtn.onclick = () => {
    logEl.textContent = '';
    if (canvasEl) canvasEl.getContext('2d').clearRect(0, 0, canvasEl.width, canvasEl.height);
  };

  if (shareBtn) shareBtn.onclick = () => {
    const b64 = btoa(unescape(encodeURIComponent(srcEl.value)));
    const url = location.origin + location.pathname + '#code=' + b64;
    navigator.clipboard.writeText(url).then(
      () => log('share URL copied to clipboard'),
      (err) => log('share copy failed: ' + err)
    );
  };

  function loadCodeFromURL() {
    const hash = location.hash.startsWith('#code=') ? location.hash.slice(6) : '';
    if (hash) {
      try {
        srcEl.value = decodeURIComponent(atob(hash));
        log('code loaded from URL');
        return;
      } catch (e) { /* fall through to the default example below */ }
    }
    srcEl.value = EXAMPLES['Hello World'];
  }

  if (exampleSelect) {
    Object.keys(EXAMPLES).forEach(name => {
      const opt = document.createElement('option');
      opt.value = name;
      opt.textContent = name;
      exampleSelect.appendChild(opt);
    });
    exampleSelect.onchange = () => { srcEl.value = EXAMPLES[exampleSelect.value] || ''; };
  }

  window.addEventListener('keydown', (ev) => {
    if ((ev.ctrlKey || ev.metaKey) && ev.key === 'Enter') { runCode(); ev.preventDefault(); }
  });

  if (location.protocol === 'file:') {
    setStatus('Error: serve over HTTP, not file://', 'error');
    log('WebAssembly requires an HTTP server; opening this file directly will not work.');
  } else {
    startWorker();
  }
})();
