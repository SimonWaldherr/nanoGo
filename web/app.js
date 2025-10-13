/* global EXAMPLES */
(function(){
  const statusEl = document.getElementById("status");
  const logEl = document.getElementById("log");
  const runBtn = document.getElementById("runBtn");
  const clearBtn = document.getElementById("clearBtn");
  const srcEl = document.getElementById("src");
  const scaleEl = document.getElementById("scale");
  const modeSelect = document.getElementById("modeSelect");
  const exampleSelect = document.getElementById("exampleSelect");
  const shareBtn = document.getElementById("shareBtn");
  const downloadBtn = document.getElementById("downloadBtn");
  const themeBtn = document.getElementById("themeBtn");

  let worker = null;
  let theme = (localStorage.getItem("nanogo_theme") || "dark");

  function setTheme(t){
    theme = t;
    if (t === "light") document.documentElement.classList.add("light");
    else document.documentElement.classList.remove("light");
    localStorage.setItem("nanogo_theme", theme);
  }
  setTheme(theme);

  themeBtn.onclick = () => setTheme(theme === "light" ? "dark" : "light");

  function setStatus(message, type = "loading") {
    statusEl.textContent = message;
    statusEl.className = `status ${type}`;
  }

  function logMessage(message) {
    logEl.textContent += message + "\n";
    logEl.scrollTop = logEl.scrollHeight;
  }

  function clearLog() {
    logEl.textContent = "Output cleared.\n";
    const c = document.getElementById('life');
    const ctx = c.getContext('2d');
    ctx.clearRect(0,0,c.width,c.height);
    document.getElementById('output').innerHTML = "";
  }
  clearBtn.onclick = clearLog;

  function drawCell(x, y, alive) {
    const canvas = document.getElementById('life');
    const ctx = canvas.getContext('2d');
    const cs = parseInt(scaleEl.value, 10) || 10;
    if (alive) ctx.fillRect(x*cs, y*cs, cs, cs);
    else ctx.clearRect(x*cs, y*cs, cs, cs);
  }

  function startWasmWorker() {
    if (worker) return;
    worker = new Worker('wasm_worker.js');
    worker.onmessage = (ev) => {
      const m = ev.data;
      if (!m || !m.type) return;
      switch (m.type) {
        case 'ready':
          setStatus('Ready! Worker is ready', 'ready');
          runBtn.disabled = false;
          logMessage('Worker ready');
          fillExamples();
          // Import code from URL hash or localStorage
          const hash = location.hash.startsWith("#code=") ? location.hash.slice(6) : "";
          if (hash) {
            try { srcEl.value = atob(decodeURIComponent(hash)) } catch {}
          } else {
            srcEl.value = localStorage.getItem("nanogo_last") || EXAMPLES["Basics"];
          }
          break;
        case 'log':
          logMessage(String(m.text));
          break;
        case 'warn':
          logMessage('WARN: ' + String(m.text));
          break;
        case 'error':
          logMessage('ERROR: ' + String(m.text));
          setStatus('Runtime error', 'error');
          break;
        case 'canvas-size': {
          const w = Number(m.w), h = Number(m.h);
          const scale = parseInt(scaleEl.value, 10);
          const canvas = document.getElementById('life');
          canvas.width = w * scale; canvas.height = h * scale;
          break;
        }
        case 'canvas-set': {
          const cx = Number(m.x), cy = Number(m.y), alive = !!m.alive;
          drawCell(cx, cy, alive);
          break;
        }
        case 'canvas-flush':
          break;
        case 'dom-setinner': {
          const el = document.getElementById(m.id);
          if (el) el.innerHTML = m.html;
          break;
        }
        case 'dom-setvalue': {
          const elv = document.getElementById(m.id);
          if (elv) elv.value = m.value;
          break;
        }
        case 'dom-addclass': {
          const ea = document.getElementById(m.id);
          if (ea) ea.classList.add(m.class);
          break;
        }
        case 'dom-removeclass': {
          const er = document.getElementById(m.id);
          if (er) er.classList.remove(m.class);
          break;
        }
        case 'open-window': {
          try { window.open(m.url, '_blank') } catch(e) { console.warn(e) }
          break;
        }
        case 'alert': {
          try { window.alert(m.text) } catch(e) { console.warn(e) }
          break;
        }
        case 'done':
          logMessage('=== Execution finished ===');
          setStatus('Ready', 'ready');
          break;
        default:
          console.log('worker:', m);
      }
    };
    worker.onerror = (e) => { setStatus('Worker error', 'error'); logMessage('Worker error: ' + e.message); };
    worker.postMessage({ type: 'init' });
  }

  function runCode() {
    try {
      if (!worker) throw new Error('worker not initialized');
      // Update canvas scale before running
      const scale = parseInt(scaleEl.value, 10);
      worker.postMessage({ type: 'set-scale', scale: scale });
      // Clear canvas for fresh run
      const c = document.getElementById('life');
      const ctx = c.getContext('2d');
      ctx.clearRect(0,0,c.width,c.height);
      // Persist
      localStorage.setItem("nanogo_last", srcEl.value);
      logMessage('=== Running user code ===');
      setStatus('Running code...', 'loading');
      const mode = modeSelect.value || 'stream';
      const t0 = performance.now();
      worker.postMessage({ type: 'run', source: srcEl.value, mode: mode, t0 });
    } catch (err) {
      setStatus('Runtime error', 'error');
      logMessage('RUNTIME ERROR: ' + err.message);
    }
  }

  runBtn.onclick = runCode;

  // Examples
  function fillExamples(){
    exampleSelect.innerHTML = "";
    Object.keys(EXAMPLES).forEach(k => {
      const opt = document.createElement('option');
      opt.value = k; opt.textContent = k;
      exampleSelect.appendChild(opt);
    });
    exampleSelect.value = 'Basics';
  }
  exampleSelect.onchange = () => {
    srcEl.value = EXAMPLES[exampleSelect.value] || EXAMPLES['Basics'];
  };

  // Share
  shareBtn.onclick = () => {
    const b64 = btoa(unescape(encodeURIComponent(srcEl.value)));
    const url = location.origin + location.pathname + "#code=" + b64;
    navigator.clipboard.writeText(url).then(()=>{
      setStatus("Sharable link copied to clipboard", "ready");
      logMessage("Share URL copied");
    }, err => {
      logMessage("Share copy failed: " + err);
    });
  };

  // Download
  downloadBtn.onclick = () => {
    const blob = new Blob([srcEl.value], {type: "text/plain"});
    const a = document.createElement('a');
    a.href = URL.createObjectURL(blob);
    a.download = "main.nanogo.go";
    a.click();
    URL.revokeObjectURL(a.href);
  };

  // Keyboard: Ctrl/Cmd + Enter
  window.addEventListener('keydown', (ev) => {
    if ((ev.ctrlKey || ev.metaKey) && ev.key === 'Enter') {
      runCode(); ev.preventDefault();
    }
  });

  if (location.protocol === 'file:') {
    setStatus("Error: Must run on HTTP server, not file://", "error");
    logMessage("ERROR: WebAssembly requires HTTP/HTTPS protocol.");
    logMessage("Start a local server: python3 -m http.server 8080");
  } else {
    startWasmWorker();
  }
})();
