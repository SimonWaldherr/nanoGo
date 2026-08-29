// playground.js — the nanoGo playground application logic.
//
// Extracted verbatim from index.html's former inline <script> (the exact
// same code, byte-for-byte, just moved to an external file) so the page's
// markup can be a plain, readable HTML document instead of one 3400-line
// file mixing markup and a 2900-line script. Behaviour is unchanged: this
// still runs as one strict-mode IIFE with no exports besides window.EXAMPLES
// (populated lazily from examples.js), exactly like the inline version did.
//
// Load order matters and is unchanged: wasm_exec.js must load before this
// file; lab.js, live_debug.js and ai.js must load after it (they read
// window.nanoGoPlayground, which this file publishes at the very end).
(function() {
      'use strict';

      // ---------------- Embedding: query-string options ----------------
      // A third party embedding this page in an <iframe> (see the Embed
      // popup below, and the README's "Embedding" section) configures it
      // entirely through query params — no build step, no separate embed
      // page to maintain. `#code=` (see loadCodeFromURL) is parsed
      // separately since it lives in the hash, not the query string. This
      // runs before initEditor() so the read-only option applies from the
      // very first paint, not as a visible flash-then-lock.
      const urlParams = new URLSearchParams(location.search);
      const isEmbedMode = urlParams.get('embed') === '1';
      const wantAutorun = urlParams.get('autorun') === '1';
      const wantReadonly = urlParams.get('readonly') === '1';
      const queryExample = urlParams.get('example');
      if (isEmbedMode) document.body.classList.add('mode-embed');

      // Example categorization with output types and descriptions
      const EXAMPLE_CONFIG = {
        'Basics': {
          output: 'console',
          description: 'Basic Go syntax, fmt package usage',
          badge: 'Fundamentals'
        },
        'Canvas': {
          output: 'canvas',
          description: 'Basic canvas drawing and pixel manipulation',
          badge: 'Graphics',
          tags: ['Graphics', 'Fundamentals']
        },
        'Channels': {
          output: 'console',
          description: 'Go channels for concurrent communication',
          badge: 'Concurrency'
        },
        'WaitGroup': {
          output: 'console',
          description: 'Synchronization with sync.WaitGroup',
          badge: 'Concurrency'
        },
        'Regexp + JSON': {
          output: 'console',
          description: 'Regular expressions and JSON handling',
          badge: 'Data Processing'
        },
        'Strings/Sort': {
          output: 'console',
          description: 'String manipulation and sorting algorithms',
          badge: 'Utilities'
        },
        'Path + UTF-8': {
          output: 'console',
          description: 'Lexical path handling and UTF-8 validation',
          badge: 'Text'
        },
        'Template + DOM': {
          output: 'dom',
          description: 'Template expansion and DOM output',
          badge: 'Web'
        },
        'Random': {
          output: 'console',
          description: 'Random number generation with math/rand',
          badge: 'Math'
        },
        'Sleep': {
          output: 'console',
          description: 'Time delays and sleep functionality',
          badge: 'Time'
        },
        'Life': {
          output: 'canvas',
          description: 'Two 100-generation Conway Life rounds with fresh random seeds',
          badge: 'Simulation',
          tags: ['Simulation', 'Animation', 'Graphics']
        },
        'HTTP Client + Storage': {
          output: 'dom',
          description: 'GET with explicit error handling and worker-session storage',
          badge: 'Web APIs'
        }
        ,
        'FizzBuzz': { output: 'console', description: 'Classic FizzBuzz printing', badge: 'Algorithms' },
        'Fibonacci': { output: 'console', description: 'Recursive Fibonacci example', badge: 'Algorithms' },
        'Prime Sieve': { output: 'console', description: 'Sieve of Eratosthenes', badge: 'Algorithms' },
        'Checkerboard': { output: 'canvas', description: 'Simple checkerboard canvas demo', badge: 'Graphics', tags: ['Graphics', 'Fundamentals'] },
        'Bouncing Ball': { output: 'canvas', description: 'Animated bouncing pixel demo', badge: 'Animation', tags: ['Animation', 'Graphics'] },
        'Plasma Waves': { output: 'canvas', description: 'Animated integer plasma field', badge: 'Graphics', tags: ['Graphics', 'Animation', 'Math'] },
        'Starfield': { output: 'canvas', description: 'Deterministic moving particle field', badge: 'Animation', tags: ['Animation', 'Graphics'] },
        'Scanner': { output: 'canvas', description: 'Sweeping scanner with pulsing targets', badge: 'Animation', tags: ['Animation', 'Graphics', 'Simulation'] },
        'Mandelbrot Set': { output: 'canvas', description: 'Escape-time fractal using floating-point arithmetic', badge: 'Graphics', tags: ['Graphics', 'Math', 'Algorithms'] },
        "Langton's Ant": { output: 'canvas', description: 'Rule-driven cellular path with animated frames', badge: 'Simulation', tags: ['Simulation', 'Animation', 'Graphics', 'Algorithms'] },
        'Sorting Visualizer': { output: 'canvas', description: 'Animated bubble-sort passes rendered as bars', badge: 'Algorithms', tags: ['Algorithms', 'Animation', 'Graphics'] },
        'Lissajous Curve': { output: 'canvas', description: 'Animated parametric sine curve with palette levels', badge: 'Math', tags: ['Math', 'Animation', 'Graphics'] },
        'Fireworks': { output: 'canvas', description: 'A deterministic color particle explosion', badge: 'Animation', tags: ['Animation', 'Graphics', 'Math'] },
        'Wave Interference': { output: 'canvas', description: 'Two animated sine waves painted as a color field', badge: 'Math', tags: ['Math', 'Animation', 'Graphics'] },
        'Pathfinding Wave': { output: 'canvas', description: 'Animated breadth-first search over a generated maze', badge: 'Algorithms', tags: ['Algorithms', 'Animation', 'Simulation', 'Graphics'] },
        'Julia Set': { output: 'canvas', description: 'A second escape-time fractal with a fixed complex constant', badge: 'Math', tags: ['Math', 'Graphics', 'Algorithms'] },
        'Rule 30': { output: 'canvas', description: 'An elementary cellular automaton growing row by row', badge: 'Simulation', tags: ['Simulation', 'Animation', 'Graphics', 'Algorithms'] },
        "Knight's Tour": { output: 'canvas', description: 'Warnsdorff-guided knight traversal of a chessboard', badge: 'Algorithms', tags: ['Algorithms', 'Animation', 'Graphics'] },
        'Metaballs': { output: 'canvas', description: 'Moving additive distance fields with palette shading', badge: 'Graphics', tags: ['Graphics', 'Animation', 'Math', 'Simulation'] }
        , 'Orbit Simulator': { output: 'canvas', description: 'Three animated bodies moving on harmonic elliptical orbits', badge: 'Simulation', tags: ['Simulation', 'Animation', 'Graphics', 'Math'] }
        , 'Sierpinski Triangle': { output: 'canvas', description: 'A Pascal-triangle fractal rendered with integer arithmetic', badge: 'Math', tags: ['Math', 'Graphics', 'Algorithms'] }
        , 'Monte Carlo Pi': { output: 'canvas', description: 'A deterministic random-sampling estimate of π', badge: 'Math', tags: ['Math', 'Animation', 'Graphics', 'Simulation'] }
        , 'Turing Machine': { output: 'canvas', description: 'A small two-state tape machine with a moving head', badge: 'Algorithms', tags: ['Algorithms', 'Animation', 'Simulation', 'Graphics'] }
        , 'HTTP Client GET': { output: 'console', description: 'GET JSON and display a response snippet', badge: 'Web' }
        , 'HTTP Client POST': { output: 'console', description: 'POST JSON with response and CORS error handling', badge: 'Web' }
        , 'HTTP Client Errors': { output: 'console', description: 'Handle a non-2xx HTTP response with text, err', badge: 'Web' }
        , 'HTTP Server Router (simulated)': { output: 'console', description: 'Model server-side routing without a network listener', badge: 'Web' }
        , 'Pipeline': { output: 'console', description: 'Simple producer -> squarer pipeline using channels', badge: 'Concurrency' }
        , 'Structs & Methods': { output: 'console', description: 'Define structs and pointer vs value methods', badge: 'Types' }
        , 'Maps & Ranges': { output: 'console', description: 'Map creation and range iteration', badge: 'Utilities' }
        , 'Timer Ticker': { output: 'console', description: 'Timers and tickers for time-based events', badge: 'Time' }
        , 'JSON Roundtrip': { output: 'console', description: 'Marshal and decode JSON with nanoGo’s facade', badge: 'Data' }
        , 'Virtual FS (os)': { output: 'console', description: 'Read, write and list files in the virtual filesystem', badge: 'OS' }
        , 'Closures': { output: 'console', description: 'Higher-order functions and closures', badge: 'Algorithms' }
        , 'Error Handling': { output: 'console', description: 'Multi-return error pattern with strconv', badge: 'Algorithms' }
        , 'Math': { output: 'console', description: 'Supported math helpers: Sqrt, Pow, Sin, Cos, Log, and more', badge: 'Math' }
        , 'Strconv': { output: 'console', description: 'strconv — number/bool/string conversions', badge: 'Utilities' }
        , 'Test: Table-driven': { output: 'console', description: 'testing.T, table cases and named subtests', badge: 'Testing' }
        , 'Benchmark: Checksum': { output: 'console', description: 'testing.B with explicit, repeatable iteration count', badge: 'Benchmarks' }
        , 'Worker Pool': { output: 'console', description: 'A small concurrent worker pool with channels and WaitGroup', badge: 'Concurrency' }
        , 'Select Statement': { output: 'console', description: 'Multiplex channels with select, a timer timeout, and a default case', badge: 'Concurrency', tags: ['Concurrency'] }
        , 'Interfaces & Polymorphism': { output: 'console', description: 'A Shape interface with Circle/Rectangle implementations and polymorphic dispatch', badge: 'Types', tags: ['Types'] }
        , 'Custom Errors': { output: 'console', description: 'A struct-based error type implementing the error interface', badge: 'Error Handling', tags: ['Error Handling'] }
        , 'Stack (LIFO)': { output: 'console', description: 'Push/pop a last-in-first-out stack built from a plain slice', badge: 'Data Structures', tags: ['Data Structures', 'Algorithms'] }
        , 'Binary Search': { output: 'console', description: 'Classic O(log n) search over a sorted slice, step-counted', badge: 'Algorithms', tags: ['Algorithms'] }
      };

      const EXAMPLE_NAMES = Object.keys(EXAMPLE_CONFIG).filter(name => name !== 'Empty');
      window.EXAMPLES = window.EXAMPLES || Object.create(null);
      let examplesLoaded = Object.keys(window.EXAMPLES).length > 0;
      let examplesLoadPromise = null;

      function getExampleSource(name) {
        return window.EXAMPLES[name];
      }

      function ensureExamplesLoaded() {
        if (examplesLoaded) return Promise.resolve(window.EXAMPLES);
        if (examplesLoadPromise) return examplesLoadPromise;
        examplesLoadPromise = new Promise((resolve, reject) => {
          const script = document.createElement('script');
          script.src = 'examples.js?11';
          script.async = true;
          script.dataset.nanogoExamples = 'true';
          script.onload = () => {
            examplesLoaded = true;
            resolve(window.EXAMPLES || Object.create(null));
          };
          script.onerror = () => {
            examplesLoadPromise = null;
            reject(new Error('Failed to load examples.js?11'));
          };
          document.body.appendChild(script);
        });
        return examplesLoadPromise;
      }

      // DOM elements
      const statusEl = document.getElementById("status");
      const logEl = document.getElementById("log");
      const runBtn = document.getElementById("runBtn");
      const clearBtn = document.getElementById("clearBtn");
      const srcEl = document.getElementById("src");
      const scaleEl = document.getElementById("scale");
      const modeSelect = document.getElementById("modeSelect");
      const shareBtn = document.getElementById("shareBtn");
      const downloadBtn = document.getElementById("downloadBtn");
      const embedBtn = document.getElementById("embedBtn");
      const embedPopup = document.getElementById("embedPopup");
      const shareMenuBtn = document.getElementById("shareMenuBtn");
      const shareMenuPopup = document.getElementById("shareMenuPopup");
      const codeMenuBtn = document.getElementById("codeMenuBtn");
      const codeMenuPopup = document.getElementById("codeMenuPopup");
      const exampleCountBadgeEl = document.getElementById("exampleCountBadge");
      const embedAutorunEl = document.getElementById("embedAutorun");
      const embedReadonlyEl = document.getElementById("embedReadonly");
      const embedSnippetEl = document.getElementById("embedSnippet");
      const embedCopyBtn = document.getElementById("embedCopyBtn");
      const embedCopyLinkBtn = document.getElementById("embedCopyLinkBtn");
      const embedPopoutLink = document.getElementById("embedPopoutLink");
      const themeBtn = document.getElementById("themeBtn");
      const themePopup = document.getElementById("themePopup");
      const sidebarEl = document.getElementById("sidebar");
      const sidebarBackdropEl = document.getElementById("sidebarBackdrop");
      const sidebarToggleBtn = document.getElementById("sidebarToggleBtn");
      const sidebarCloseBtn = document.getElementById("sidebarCloseBtn");
      const tagFilters = document.getElementById("tagFilters");
      const examplesList = document.getElementById("examplesList");
      const exampleSearch = document.getElementById("exampleSearch");

      const stopBtn = document.getElementById("stopBtn");
      const formatBtn = document.getElementById("formatBtn");
      const vetBtn = document.getElementById("vetBtn");
      const testBtn = document.getElementById("testBtn");
      const testFilterEl = document.getElementById("testFilter");
      const inspectBtn = document.getElementById("inspectBtn");
      const copyLogBtn = document.getElementById("copyLogBtn");
      const fileInput = document.getElementById("fileInput");
      const fontSizeSelect = document.getElementById("fontSize");
      const execTimeEl = document.getElementById("execTime");

      // IDE workspace state. The editor remains the fast single-document
      // path, while a second file promotes the session to a VFS-backed module
      // without losing the familiar tabs, examples, sharing, or download
      // flows. Snapshots are local-only and never leave this browser unless
      // the user explicitly runs the project against the WASM worker.
      const workspaceBarEl = document.getElementById('workspaceBar');
      const workspaceNameEl = document.getElementById('workspaceName');
      const workspaceStatusEl = document.getElementById('workspaceStatus');
      const workspaceTabsEl = document.getElementById('workspaceTabs');
      const workspaceAddBtn = document.getElementById('workspaceAddBtn');
      const workspaceCheckBtn = document.getElementById('workspaceCheckBtn');
      const workspaceRunBtn = document.getElementById('workspaceRunBtn');
      const workspaceInspectBtn = document.getElementById('workspaceInspectBtn');
      const workspaceMetaEl = document.getElementById('workspaceMeta');
      const workspaceDiagnosticsEl = document.getElementById('workspaceDiagnostics');
      const commandPaletteEl = document.getElementById('commandPalette');
      const commandPaletteInputEl = document.getElementById('commandPaletteInput');
      const commandPaletteListEl = document.getElementById('commandPaletteList');
      const WORKSPACE_STORAGE_KEY = 'nanogo_workspace_v1';
      let workspaceFiles = new Map([['main.go', '']]);
      let activeFilePath = 'main.go';
      let workspaceModulePath = 'nanogo.local/workspace';
      let workspaceSaveTimer = null;
      let workspaceDirty = false;

      // Inspector elements
      const astBtn = document.getElementById("astBtn");
      const astTreeEl = document.getElementById("astTree");
      const astMetaEl = document.getElementById("astMeta");
      const callGraphBtn = document.getElementById("callGraphBtn");
      const callGraphListEl = document.getElementById("callGraphList");
      const callGraphMetaEl = document.getElementById("callGraphMeta");
      const callGraphDiagramEl = document.getElementById("callGraphDiagram");
      const callGraphViewSwitchEl = document.getElementById("callGraphViewSwitch");
      const symbolDetailsEl = document.getElementById("symbolDetails");
      const symbolPanelBadgeEl = document.getElementById("symbolPanelBadge");
      const benchBtn = document.getElementById("benchBtn");
      const benchIterationsEl = document.getElementById("benchIterations");
      const benchResultsEl = document.getElementById("benchResults");
      const benchMetaEl = document.getElementById("benchMeta");
      const traceToggle = document.getElementById("traceToggle");
      const traceTableEl = document.getElementById("traceTable");
      const traceMetaEl = document.getElementById("traceMeta");
      const traceSectionEl = document.getElementById("traceSection");
      const heatmapToggle = document.getElementById("heatmapToggle");
      const heatmapListEl = document.getElementById("heatmapList");
      const heatmapMetaEl = document.getElementById("heatmapMeta");
      const heatmapSectionEl = document.getElementById("heatmapSection");
      const heatmapClearBtn = document.getElementById("heatmapClearBtn");
      const benchSectionEl = document.getElementById("benchSection");
      const statWallEl = document.getElementById("statWall");
      const statInterpEl = document.getElementById("statInterp");
      const statStepsEl = document.getElementById("statSteps");
      const statStepsRateEl = document.getElementById("statStepsRate");
      let wasmCapabilities = {};
      let activeRunSource = '';
      let lastRuntimeSource = '';
      let lastRuntimeVariables = [];

      // Whether the CURRENT run has already auto-switched the output tab in
      // response to something the guest program actually did (drew to the
      // canvas, wrote HTML). Reset at the start of every run (see runCode/
      // runWorkspace) and consumed at most once per run by
      // autoSwitchOutputPanel below, so a long-running animation doesn't
      // keep yanking focus away from a tab the user deliberately switched to
      // mid-run, but the first real signal always wins over whatever tab
      // happened to be showing (including one preselected from static
      // per-example metadata) -- unlike that metadata, this reflects what the
      // program is actually doing right now, which also covers hand-written
      // code that was never in the examples list at all.
      let autoSwitchedOutputThisRun = false;
      function autoSwitchOutputPanel(panel) {
        if (autoSwitchedOutputThisRun) return;
        autoSwitchedOutputThisRun = true;
        showOutputPanel(panel);
      }

      function applyFontSize(size) {
        const px = size + 'px';
        if (editor && editor.getWrapperElement) {
          editor.getWrapperElement().style.fontSize = px;
          editor.refresh();
        }
        if (srcEl) srcEl.style.fontSize = px;
      }

      // CodeMirror editor (initialized from the textarea). The CDN scripts
      // load with `defer`, so they may not have executed yet when this inline
      // script runs — retry once the DOM (and deferred scripts) are ready.
      let editor = null;

      function isWorkspaceProject() {
        return workspaceFiles.size > 1 || workspaceFiles.has('go.mod');
      }

      function workspaceFileEntries() {
        saveActiveWorkspaceSource();
        return Array.from(workspaceFiles, ([path, source]) => ({ path, source }));
      }

      function saveActiveWorkspaceSource() {
        if (!activeFilePath) return;
        const source = editor && typeof editor.getValue === 'function'
          ? editor.getValue() : (srcEl ? srcEl.value : '');
        workspaceFiles.set(activeFilePath, String(source || ''));
      }

      function persistWorkspace() {
        if (workspaceSaveTimer) clearTimeout(workspaceSaveTimer);
        workspaceSaveTimer = setTimeout(() => {
          workspaceSaveTimer = null;
          saveActiveWorkspaceSource();
          try {
            localStorage.setItem(WORKSPACE_STORAGE_KEY, JSON.stringify({
              modulePath: workspaceModulePath,
              active: activeFilePath,
              files: workspaceFileEntries()
            }));
          } catch (e) { /* private browsing/storage quota: keep editing */ }
        }, 120);
      }

      function fileLabel(filePath) {
        const name = filePath.split('/').pop() || filePath;
        return name === 'go.mod' ? '◇ ' + name : '▱ ' + name;
      }

      function renderWorkspaceTabs() {
        if (!workspaceTabsEl) return;
        workspaceTabsEl.innerHTML = '';
        for (const filePath of workspaceFiles.keys()) {
          const tab = document.createElement('button');
          tab.type = 'button';
          tab.className = 'workspace-tab' + (filePath === activeFilePath ? ' active' : '');
          tab.setAttribute('role', 'tab');
          tab.setAttribute('aria-selected', filePath === activeFilePath ? 'true' : 'false');
          tab.title = filePath + ' · double-click rename · middle-click close';
          tab.dataset.path = filePath;
          tab.textContent = fileLabel(filePath);
          tab.addEventListener('click', () => openWorkspaceFile(filePath));
          tab.addEventListener('dblclick', ev => { ev.preventDefault(); renameWorkspaceFile(filePath); });
          tab.addEventListener('auxclick', ev => { if (ev.button === 1) { ev.preventDefault(); removeWorkspaceFile(filePath); } });
          workspaceTabsEl.appendChild(tab);
        }
        if (workspaceNameEl) workspaceNameEl.textContent = workspaceModulePath.split('/').pop() || 'IDE workspace';
        if (workspaceStatusEl) {
          const count = workspaceFiles.size;
          workspaceStatusEl.textContent = count + ' file' + (count === 1 ? '' : 's') + ' · ' + activeFilePath + (workspaceDirty ? ' · unsaved' : '');
        }
        if (workspaceRunBtn) workspaceRunBtn.classList.toggle('active', isWorkspaceProject());
      }

      function setEditorModeForFile(filePath) {
        if (editor && editor.setOption) editor.setOption('mode', filePath === 'go.mod' ? 'text/plain' : 'text/x-go');
      }

      function openWorkspaceFile(filePath) {
        if (!workspaceFiles.has(filePath)) return;
        saveActiveWorkspaceSource();
        activeFilePath = filePath;
        const source = workspaceFiles.get(filePath) || '';
        setEditorModeForFile(filePath);
        if (editor && typeof editor.setValue === 'function') editor.setValue(source);
        else if (srcEl) srcEl.value = source;
        renderWorkspaceTabs();
        persistWorkspace();
      }

      function replaceWorkspace(files, active) {
        const next = new Map();
        (files || []).forEach(file => {
          if (!file || typeof file.path !== 'string') return;
          const clean = file.path.replace(/\\/g, '/').replace(/^\/+/, '');
          if (!clean || clean === '.' || clean === '..' || clean.startsWith('../')) return;
          if (clean !== 'go.mod' && !clean.endsWith('.go')) return;
          next.set(clean, String(file.source || ''));
        });
        if (!next.size) next.set('main.go', '');
        workspaceFiles = next;
        activeFilePath = workspaceFiles.has(active) ? active : workspaceFiles.keys().next().value;
        workspaceDirty = false;
        setEditorModeForFile(activeFilePath);
        const source = workspaceFiles.get(activeFilePath) || '';
        if (editor && typeof editor.setValue === 'function') editor.setValue(source);
        else if (srcEl) srcEl.value = source;
        workspaceDirty = false;
        renderWorkspaceTabs();
        persistWorkspace();
      }

      function resetWorkspace(source, filePath) {
        workspaceModulePath = 'nanogo.local/workspace';
        replaceWorkspace([{ path: filePath || 'main.go', source: String(source || '') }], filePath || 'main.go');
      }

      function loadPersistedWorkspace() {
        try {
          const raw = localStorage.getItem(WORKSPACE_STORAGE_KEY);
          if (!raw) return false;
          const saved = JSON.parse(raw);
          if (!saved || !Array.isArray(saved.files) || !saved.files.length) return false;
          workspaceModulePath = typeof saved.modulePath === 'string' && saved.modulePath.trim()
            ? saved.modulePath.trim() : 'nanogo.local/workspace';
          replaceWorkspace(saved.files, saved.active);
          logMessage('💾 Workspace restored: ' + workspaceFiles.size + ' file(s)', 'system');
          return true;
        } catch (e) { return false; }
      }

      function addWorkspaceFile(filePath, source) {
        const clean = String(filePath || '').replace(/\\/g, '/').replace(/^\/+/, '');
        if (!clean || (clean !== 'go.mod' && !clean.endsWith('.go')) || clean.includes('..')) return false;
        saveActiveWorkspaceSource();
        const defaultSource = clean === 'go.mod' ? 'module ' + workspaceModulePath + '\n' : 'package main\n';
        workspaceFiles.set(clean, String(source || defaultSource));
        workspaceDirty = true;
        openWorkspaceFile(clean);
        renderWorkspaceTabs();
        persistWorkspace();
        return true;
      }

      function renameWorkspaceFile(oldPath) {
        const nextRaw = window.prompt('Rename workspace file', oldPath);
        if (!nextRaw || nextRaw === oldPath) return;
        const next = nextRaw.replace(/\\/g, '/').replace(/^\/+/, '');
        if (!next || next.includes('..') || (next !== 'go.mod' && !next.endsWith('.go')) || workspaceFiles.has(next)) {
          logMessage('📁 Invalid or duplicate workspace path', 'warn');
          return;
        }
        const replacement = new Map();
        workspaceFiles.forEach((source, path) => replacement.set(path === oldPath ? next : path, source));
        workspaceFiles = replacement;
        if (activeFilePath === oldPath) activeFilePath = next;
        workspaceDirty = true;
        renderWorkspaceTabs();
        persistWorkspace();
      }

      function removeWorkspaceFile(filePath) {
        if (workspaceFiles.size <= 1) {
          logMessage('📁 Keep at least one workspace file', 'warn');
          return;
        }
        workspaceFiles.delete(filePath);
        if (activeFilePath === filePath) {
          activeFilePath = workspaceFiles.keys().next().value;
          setEditorModeForFile(activeFilePath);
          const source = workspaceFiles.get(activeFilePath) || '';
          if (editor && typeof editor.setValue === 'function') editor.setValue(source);
          else if (srcEl) srcEl.value = source;
        }
        workspaceDirty = true;
        renderWorkspaceTabs();
        persistWorkspace();
        logMessage('📁 Removed ' + filePath, 'system');
      }

      function workspacePayload() {
        saveActiveWorkspaceSource();
        return { files: workspaceFileEntries(), modulePath: workspaceModulePath };
      }

      function showWorkspaceResult(result, error) {
        if (workspaceMetaEl) workspaceMetaEl.textContent = error ? 'error' : (result && result.workspace
          ? result.workspace.packages.length + ' package(s)' : '');
        if (!workspaceDiagnosticsEl) return;
        if (error) {
          workspaceDiagnosticsEl.innerHTML = '<div class="inspector-error">' + escapeHtml(error) + '</div>';
          return;
        }
        const info = result && (result.workspace || result);
        if (!info) {
          workspaceDiagnosticsEl.innerHTML = '<p class="inspector-note">No workspace metadata returned.</p>';
          return;
        }
        if (info.module && info.module !== workspaceModulePath) {
          workspaceModulePath = info.module;
          renderWorkspaceTabs();
          persistWorkspace();
        }
        const packageRows = (info.packages || []).map(pkg => {
          const imports = (pkg.imports || []).length ? ' · imports ' + pkg.imports.join(', ') : '';
          return '<li><code>' + escapeHtml(pkg.name || '?') + '</code> <span class="workspace-package-path">' + escapeHtml(pkg.dir || '') + '</span> · ' + (pkg.files || []).length + ' file(s)' + escapeHtml(imports) + '</li>';
        }).join('');
        workspaceDiagnosticsEl.innerHTML =
          '<div class="workspace-summary"><span><strong>' + escapeHtml(info.module || workspaceModulePath) + '</strong></span><span>' + info.files + ' file(s)</span><span>' + (info.packages || []).length + ' package(s)</span></div>' +
          (packageRows ? '<ul class="workspace-package-list">' + packageRows + '</ul>' : '<p class="inspector-note">No packages discovered.</p>');
      }

      function requestWorkspaceCheck() {
        if (!worker) { logMessage('Worker not ready', 'warn'); return; }
        const payload = workspacePayload();
        if (workspaceCheckBtn) { workspaceCheckBtn.disabled = true; workspaceCheckBtn.textContent = 'Checking…'; }
        if (workspaceInspectBtn) { workspaceInspectBtn.disabled = true; workspaceInspectBtn.textContent = 'Checking…'; }
        showOutputPanel('inspector');
        worker.postMessage({ type: 'workspace-check', files: payload.files, modulePath: payload.modulePath });
      }

      function requestWorkspaceTests() {
        if (!worker) { logMessage('Worker not ready', 'warn'); return; }
        const payload = workspacePayload();
        showOutputPanel('inspector');
        if (testBtn) { testBtn.disabled = true; testBtn.textContent = '🧪 Testing…'; }
        worker.postMessage({ type: 'workspace-test', files: payload.files, modulePath: payload.modulePath, filter: testFilterEl ? testFilterEl.value.trim() : '' });
      }

      function runWorkspace() {
        if (!worker) { logMessage('Worker not ready', 'warn'); return; }
        autoSwitchedOutputThisRun = false;
        const payload = workspacePayload();
        activeRunSource = getSource();
        const scale = parseInt(scaleEl.value, 10);
        worker.postMessage({ type: 'set-scale', scale: scale });
        const c = document.getElementById('life');
        if (c) c.getContext('2d').clearRect(0, 0, c.width, c.height);
        if (execTimeEl) execTimeEl.textContent = '';
        clearErrorHighlight();
        breakpointHits.clear();
        refreshBreakpointMarkers();
        clearHeatmap();
        logMessage('🚀 Running workspace…', 'system');
        setStatus('Running workspace...', 'loading');
        if (workspaceRunBtn) { workspaceRunBtn.disabled = true; workspaceRunBtn.textContent = '⏳ Project…'; }
        const wantTrace = !!(traceToggle && traceToggle.checked);
        const wantProfile = !!(heatmapToggle && heatmapToggle.checked);
        const t0 = performance.now();
        worker.postMessage({ type: 'workspace-run', files: payload.files, modulePath: payload.modulePath, trace: wantTrace, profile: wantProfile, t0 });
      }

      // A small command palette gives the playground IDE muscle memory: one
      // shortcut exposes the same actions as the compact toolbar, including
      // project-aware Run/Check and the existing analysis surfaces.
      let commandPaletteIndex = 0;
      function commandDefinitions() {
        return [
          { label: isWorkspaceProject() ? 'Run workspace' : 'Run active file', hint: 'Ctrl/⌘+Enter', run: runCode },
          { label: 'Check workspace imports', hint: 'go list', run: requestWorkspaceCheck },
          { label: 'Format source', hint: 'gofmt', run: () => formatBtn && formatBtn.click() },
          { label: 'Vet source', hint: 'static analysis', run: () => vetBtn && vetBtn.click() },
          { label: 'Run tests', hint: 'go test', run: () => testBtn && testBtn.click() },
          { label: 'Open Inspector', hint: 'AST · trace · SDK', run: () => showOutputPanel('inspector') },
          { label: 'Visualize call graph', hint: 'Mermaid', run: () => requestCallGraph() },
          { label: 'Browse examples', hint: 'sidebar', run: () => setSidebarOpen(true) }
        ];
      }

      function closeCommandPalette() {
        if (!commandPaletteEl) return;
        commandPaletteEl.hidden = true;
      }

      function renderCommandPalette() {
        if (!commandPaletteListEl) return;
        const query = (commandPaletteInputEl && commandPaletteInputEl.value || '').trim().toLowerCase();
        const commands = commandDefinitions().filter(cmd => !query || (cmd.label + ' ' + cmd.hint).toLowerCase().includes(query));
        commandPaletteIndex = Math.min(commandPaletteIndex, Math.max(0, commands.length - 1));
        commandPaletteListEl.innerHTML = '';
        commands.forEach((cmd, index) => {
          const button = document.createElement('button');
          button.type = 'button';
          button.className = 'command-item' + (index === commandPaletteIndex ? ' active' : '');
          button.setAttribute('role', 'option');
          button.setAttribute('aria-selected', index === commandPaletteIndex ? 'true' : 'false');
          button.innerHTML = '<span>' + escapeHtml(cmd.label) + '</span><small>' + escapeHtml(cmd.hint) + '</small>';
          button.addEventListener('click', () => { closeCommandPalette(); cmd.run(); });
          commandPaletteListEl.appendChild(button);
        });
      }

      function openCommandPalette() {
        if (!commandPaletteEl) return;
        commandPaletteEl.hidden = false;
        commandPaletteIndex = 0;
        if (commandPaletteInputEl) { commandPaletteInputEl.value = ''; commandPaletteInputEl.focus(); }
        renderCommandPalette();
      }

      if (commandPaletteInputEl) commandPaletteInputEl.addEventListener('input', () => {
        commandPaletteIndex = 0;
        renderCommandPalette();
      });
      if (commandPaletteEl) commandPaletteEl.addEventListener('click', ev => {
        if (ev.target === commandPaletteEl) closeCommandPalette();
      });

      renderWorkspaceTabs();

      function initEditor() {
        if (editor || typeof CodeMirror === 'undefined' || !srcEl) return;
        try {
          editor = CodeMirror.fromTextArea(srcEl, {
            mode: 'text/x-go',
            lineNumbers: true,
            gutters: ['CodeMirror-linenumbers', 'nanogo-breakpoints'],
            indentUnit: 2,
            tabSize: 2,
            lineWrapping: true,
            viewportMargin: 12,
            readOnly: wantReadonly ? 'nocursor' : false
          });
          editor.setSize('100%', '100%');
          if (fontSizeSelect) applyFontSize(fontSizeSelect.value);
          // Editing invalidates any error highlight left over from the last
          // run — the line numbers it was anchored to may no longer mean
          // the same thing once the user starts changing the code.
          editor.on('change', () => {
            saveActiveWorkspaceSource();
            workspaceDirty = true;
            clearErrorHighlight(); clearHeatmap();
            invalidateSymbolSelection();
            renderWorkspaceTabs();
            persistWorkspace();
            document.dispatchEvent(new CustomEvent('nanogo:source-change'));
          });
          editor.on('gutterClick', (cm, line, gutter) => {
            if (gutter === 'nanogo-breakpoints') toggleBreakpoint(line + 1);
          });
          editor.on('mousedown', (cm, event) => {
            if (event.button !== 0) return;
            const pos = cm.coordsChar({ left: event.clientX, top: event.clientY }, 'window');
            // Wait until CodeMirror has completed its own cursor/selection
            // handling, then inspect the token that was actually clicked.
            setTimeout(() => inspectEditorSymbolAt(pos), 0);
          });
          // CodeMirror creates its own hidden input for capturing keystrokes;
          // it does not inherit these attributes from the original textarea,
          // so mobile keyboards (iOS) would otherwise autocorrect/autocapitalize
          // Go code and offer a QuickType bar full of irrelevant suggestions.
          const input = editor.getInputField && editor.getInputField();
          if (input) {
            input.setAttribute('autocorrect', 'off');
            input.setAttribute('autocapitalize', 'off');
            input.setAttribute('autocomplete', 'off');
            input.setAttribute('spellcheck', 'false');
          }
        } catch (e) { editor = null; }
      }
      initEditor();
      if (!editor) document.addEventListener('DOMContentLoaded', initEditor, { once: true });
      setEditorModeForFile(activeFilePath);

      // Keep the sticky toolbar/tabs offset in sync with the header's real
      // (variable, wrapping-dependent) height, so the Run/Stop/Vet/Test/
      // Inspect controls and the output tabs never drift out from under the
      // sticky header no matter how the page scrolls.
      (function setupStickyOffsets() {
        const headerEl = document.querySelector('.header');
        if (!headerEl) return;
        function update() {
          document.documentElement.style.setProperty('--header-h', headerEl.offsetHeight + 'px');
        }
        update();
        window.addEventListener('resize', update);
        window.addEventListener('orientationchange', update);
        if (typeof ResizeObserver !== 'undefined') new ResizeObserver(update).observe(headerEl);
      })();

      function getSource() { return (editor && typeof editor.getValue === 'function') ? editor.getValue() : (srcEl ? srcEl.value : ''); }
      function setSource(v) {
        const source = String(v == null ? '' : v);
        if (editor && typeof editor.setValue === 'function') editor.setValue(source);
        if (srcEl) srcEl.value = source;
        workspaceFiles.set(activeFilePath || 'main.go', source);
        workspaceDirty = true;
        renderWorkspaceTabs();
        persistWorkspace();
      }

      // Breakpoints are source-linked program points. A debug run sends their
      // line numbers to the interpreter, which records bounded hit events in
      // the trace; replay can then jump through those exact source lines.
      const breakpointHandles = new Set();
      const breakpointHits = new Map();
      function breakpointEntries() {
        if (!editor) return [];
        const entries = [];
        breakpointHandles.forEach((handle) => {
          const index = editor.getLineNumber(handle);
          if (index < 0) {
            breakpointHandles.delete(handle);
            return;
          }
          entries.push({ handle, line: index + 1 });
        });
        entries.sort((a, b) => a.line - b.line);
        return entries;
      }
      function breakpointLineNumbers() {
        return breakpointEntries().map((entry) => entry.line);
      }
      function breakpointMarker(line) {
        const hits = breakpointHits.get(line) || 0;
        const marker = document.createElement('span');
        marker.className = 'nanogo-breakpoint-marker' + (hits ? ' hit' : '');
        marker.title = hits ? 'Breakpoint · ' + hits + ' hit(s)' : 'Breakpoint';
        marker.setAttribute('aria-label', marker.title);
        return marker;
      }
      function refreshBreakpointMarkers() {
        breakpointEntries().forEach((entry) => {
          editor.setGutterMarker(entry.handle, 'nanogo-breakpoints', breakpointMarker(entry.line));
        });
      }
      function applyBreakpointHits(events) {
        breakpointHits.clear();
        (events || []).forEach((event) => {
          if (event.kind !== 'breakpoint' || !event.line) return;
          breakpointHits.set(event.line, (breakpointHits.get(event.line) || 0) + 1);
        });
        refreshBreakpointMarkers();
        document.dispatchEvent(new CustomEvent('nanogo:breakpoint-hits', {
          detail: { hits: Array.from(breakpointHits, ([line, count]) => ({ line, count })) }
        }));
      }
      function toggleBreakpoint(line) {
        if (!editor || !line) return false;
        const existing = breakpointEntries().find((entry) => entry.line === line);
        if (existing) {
          breakpointHandles.delete(existing.handle);
          breakpointHits.delete(line);
          editor.setGutterMarker(existing.handle, 'nanogo-breakpoints', null);
        } else {
          const handle = editor.getLineHandle(line - 1);
          if (!handle) return false;
          breakpointHandles.add(handle);
          editor.setGutterMarker(handle, 'nanogo-breakpoints', breakpointMarker(line));
        }
        document.dispatchEvent(new CustomEvent('nanogo:breakpoints-change', { detail: { lines: breakpointLineNumbers() } }));
        return breakpointLineNumbers().includes(line);
      }
      document.addEventListener('nanogo:source-change', () => {
        breakpointHits.clear();
        refreshBreakpointMarkers();
      });

      // Output panels
      const canvasPanel = document.getElementById("canvasPanel");
      const consolePanel = document.getElementById("consolePanel");
      const domPanel = document.getElementById("domPanel");
      const inspectorPanel = document.getElementById("inspectorPanel");
      const outputTabs = document.querySelectorAll('.output-tab');

      let worker = null;
      let currentExample = null;
      let selectedTag = null;
      let theme = (urlParams.get('theme') || localStorage.getItem("nanogo_theme") || "dark");

      function setTheme(t) {
        theme = t || 'dark';
        // remove any existing theme-* classes to avoid leftover conflicts
        const cls = Array.from(document.documentElement.classList);
        cls.forEach(c => {
          if (c.startsWith('theme-')) document.documentElement.classList.remove(c);
        });
        // apply new theme class
        document.documentElement.classList.add('theme-' + theme);
        localStorage.setItem("nanogo_theme", theme);
        // update button icon
        if (themeBtn) {
          const icons = { dark: '🌗', light: '🌙', solar: '☀️', dracula: '🖤', sepia: '🟤', monokai: '🟩', ocean: '🌊', forest: '🌲', midnight: '🌌', pastel: '🌸', 'high-contrast': '⚫' };
          themeBtn.textContent = icons[theme] || '🎨';
        }
      }
      setTheme(theme);

      // Sidebar drawer: an overlay (see styles.css .sidebar) rather than a
      // grid column, so opening it never reflows the editor/CodeMirror
      // underneath. Open state persists like the theme does, except in embed
      // mode, where the drawer stays closed and hidden regardless of any
      // state saved from a previous non-embed visit to the same origin.
      let sidebarOpen = !isEmbedMode && localStorage.getItem('nanogo_sidebar_open') === 'true';
      function setSidebarOpen(open) {
        sidebarOpen = open;
        if (sidebarEl) { sidebarEl.classList.toggle('open', open); sidebarEl.setAttribute('aria-hidden', open ? 'false' : 'true'); }
        if (sidebarBackdropEl) sidebarBackdropEl.classList.toggle('open', open);
        if (sidebarToggleBtn) sidebarToggleBtn.setAttribute('aria-expanded', open ? 'true' : 'false');
        if (!isEmbedMode) localStorage.setItem('nanogo_sidebar_open', open ? 'true' : 'false');
      }
      setSidebarOpen(sidebarOpen);
      if (sidebarToggleBtn) sidebarToggleBtn.addEventListener('click', () => setSidebarOpen(!sidebarOpen));
      if (sidebarCloseBtn) sidebarCloseBtn.addEventListener('click', () => setSidebarOpen(false));
      if (sidebarBackdropEl) sidebarBackdropEl.addEventListener('click', () => setSidebarOpen(false));
      document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape' && sidebarOpen) setSidebarOpen(false); });

      // Direct feedback after the drawer-only redesign: with the examples
      // list reachable only through one icon-only hamburger, nobody found
      // it. #sidebarToggleBtn is now a labeled "📚 Examples" control (see
      // index.html), which helps on its own, but the strongest fix is to
      // show the drawer itself once, unprompted, so a first-time visitor
      // (or an existing one who never noticed the drawer before this
      // change) sees the example list without having to click anything.
      // Gated on a dedicated flag rather than reusing nanogo_sidebar_open,
      // so it fires exactly once per browser regardless of whatever open/
      // closed preference that other flag already holds. Called once WASM
      // is ready (see the 'ready' case in handleWorkerMessage) so it can't
      // race the very first paint. Embed mode is exempt, same as every
      // other sidebar behavior -- see setSidebarOpen's own comment.
      function introduceExamplesDrawer() {
        if (isEmbedMode) return;
        if (localStorage.getItem('nanogo_examples_intro_shown') === 'true') return;
        localStorage.setItem('nanogo_examples_intro_shown', 'true');
        setSidebarOpen(true);
      }

      // Theme popup control
      function closeThemePopup() {
        if (!themePopup) return;
        themePopup.setAttribute('aria-hidden', 'true');
      }
      function openThemePopup() {
        if (!themePopup) return;
        themePopup.setAttribute('aria-hidden', 'false');
        // Sync aria-pressed with current theme and focus the selected option for accessibility
        try {
          const opts = themePopup.querySelectorAll('.theme-option');
          opts.forEach(o => o.setAttribute('aria-pressed', o.getAttribute('data-theme') === theme ? 'true' : 'false'));
          const sel = themePopup.querySelector('.theme-option[aria-pressed="true"]');
          if (sel) {
            sel.focus();
            sel.classList.add('focused');
            setTimeout(() => sel.classList.remove('focused'), 400);
          }
        } catch (e) { /* ignore */ }
      }
      // ---------------- Share / Code menus -----------------------------
      // Two small always-there-but-collapsed menus (see the toolbar
      // redesign after user feedback that the flat button row had grown
      // too large): Share/Embed/Download/Upload live behind #shareMenuBtn,
      // Format/Vet/Test/Clear behind #codeMenuBtn. Both reuse the exact
      // .theme-popup show/hide mechanism (aria-hidden toggling, absolute
      // positioning) already proven by the theme picker and the embed
      // popup below -- .menu-popup only changes the popup's internal
      // layout (a vertical list of full-width rows) via CSS, not how it
      // opens/closes. Every row keeps the exact id and click handler it
      // always had; only their DOM position (now inside a popup instead
      // of directly in the toolbar) and this file's mutual-exclusion
      // wiring are new.
      function closeShareMenuPopup() {
        if (!shareMenuPopup) return;
        shareMenuPopup.setAttribute('aria-hidden', 'true');
        if (shareMenuBtn) shareMenuBtn.setAttribute('aria-expanded', 'false');
      }
      function openShareMenuPopup() {
        if (!shareMenuPopup) return;
        shareMenuPopup.setAttribute('aria-hidden', 'false');
        if (shareMenuBtn) shareMenuBtn.setAttribute('aria-expanded', 'true');
      }
      function closeCodeMenuPopup() {
        if (!codeMenuPopup) return;
        codeMenuPopup.setAttribute('aria-hidden', 'true');
        if (codeMenuBtn) codeMenuBtn.setAttribute('aria-expanded', 'false');
      }
      function openCodeMenuPopup() {
        if (!codeMenuPopup) return;
        codeMenuPopup.setAttribute('aria-hidden', 'false');
        if (codeMenuBtn) codeMenuBtn.setAttribute('aria-expanded', 'true');
      }
      // Only one of Theme/Share/Code (plus the Embed popup nested inside
      // Share) is ever open at once -- each trigger closes the other two
      // top-level menus before toggling its own, the same pairwise pattern
      // this file already used between Theme and Embed.
      if (shareMenuBtn) shareMenuBtn.onclick = () => {
        closeThemePopup();
        closeCodeMenuPopup();
        const hidden = !shareMenuPopup || shareMenuPopup.getAttribute('aria-hidden') !== 'false';
        if (hidden) openShareMenuPopup(); else closeShareMenuPopup();
      };
      if (codeMenuBtn) codeMenuBtn.onclick = () => {
        closeThemePopup();
        closeShareMenuPopup();
        const hidden = !codeMenuPopup || codeMenuPopup.getAttribute('aria-hidden') !== 'false';
        if (hidden) openCodeMenuPopup(); else closeCodeMenuPopup();
      };
      if (shareMenuPopup) {
        // shareBtn/downloadBtn/the upload label all keep their existing
        // onclick handlers (further down this file) unmodified; this just
        // closes the menu after whichever one of them just ran. embedBtn is
        // deliberately excluded -- its own handler (below) opens embedPopup
        // and must not have this generic close race with that.
        [shareBtn, downloadBtn].forEach(btn => {
          if (btn) btn.addEventListener('click', closeShareMenuPopup);
        });
        const uploadLabel = fileInput ? fileInput.closest('.upload-control') : null;
        if (uploadLabel) uploadLabel.addEventListener('click', closeShareMenuPopup);
        document.addEventListener('click', (ev) => {
          if (shareMenuPopup.getAttribute('aria-hidden') === 'false') {
            const path = ev.composedPath ? ev.composedPath() : (ev.path || []);
            if (!path.includes(shareMenuPopup) && ev.target !== shareMenuBtn) closeShareMenuPopup();
          }
        });
        document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') closeShareMenuPopup(); });
      }
      if (codeMenuPopup) {
        [formatBtn, vetBtn, testBtn, clearBtn].forEach(btn => {
          if (btn) btn.addEventListener('click', closeCodeMenuPopup);
        });
        document.addEventListener('click', (ev) => {
          if (codeMenuPopup.getAttribute('aria-hidden') === 'false') {
            const path = ev.composedPath ? ev.composedPath() : (ev.path || []);
            if (!path.includes(codeMenuPopup) && ev.target !== codeMenuBtn) closeCodeMenuPopup();
          }
        });
        document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') closeCodeMenuPopup(); });
      }

      if (themeBtn) themeBtn.onclick = (e) => {
        if (!themePopup) return;
        if (embedPopup) embedPopup.setAttribute('aria-hidden', 'true');
        closeShareMenuPopup();
        closeCodeMenuPopup();
        const hidden = themePopup.getAttribute('aria-hidden') !== 'false';
        if (hidden) openThemePopup(); else closeThemePopup();
      };

      // Wire theme option buttons and render swatches. Swatch colors are read
      // from each theme's real --bg/--accent custom properties (briefly
      // switching <html>'s theme class per swatch, synchronously, so nothing
      // paints in between) rather than a hand-maintained hex table — the old
      // table had already drifted from the actual CSS (e.g. dark's --bg is
      // #080c14 in styles.css but was hardcoded here as #0a0c10), and a
      // second source of truth like that can only drift further over time.
      function computeThemeMeta(themeNames) {
        const root = document.documentElement;
        const active = Array.from(root.classList).filter(c => c.startsWith('theme-'));
        active.forEach(c => root.classList.remove(c));
        const meta = {};
        themeNames.forEach(t => {
          root.classList.add('theme-' + t);
          const cs = getComputedStyle(root);
          meta[t] = { bg: cs.getPropertyValue('--bg').trim(), accent: cs.getPropertyValue('--accent').trim() };
          root.classList.remove('theme-' + t);
        });
        active.forEach(c => root.classList.add(c));
        return meta;
      }

      if (themePopup) {
        const themeNames = Array.from(themePopup.querySelectorAll('.theme-option')).map(btn => btn.getAttribute('data-theme'));
        const THEME_META = computeThemeMeta(themeNames);

        themePopup.querySelectorAll('.theme-option').forEach(btn => {
          const t = btn.getAttribute('data-theme');
          const meta = THEME_META[t] || THEME_META.dark;
          // render swatch + label
          btn.innerHTML = '';
          const sw = document.createElement('span');
          sw.className = 'swatch';
          sw.style.background = meta.bg;
          sw.style.borderColor = meta.accent;
          sw.title = t;
          const lbl = document.createElement('span');
          lbl.className = 'label';
          lbl.textContent = t.split('-').map(s=>s[0].toUpperCase()+s.slice(1)).join(' ');
          btn.appendChild(sw);
          btn.appendChild(lbl);

          // reflect current selection
          btn.setAttribute('aria-pressed', theme === t ? 'true' : 'false');

          btn.addEventListener('click', (ev) => {
            setTheme(t);
            // update aria-pressed on options
            themePopup.querySelectorAll('.theme-option').forEach(o => o.setAttribute('aria-pressed', 'false'));
            btn.setAttribute('aria-pressed', 'true');
            closeThemePopup();
          });
        });

        // click outside closes popup
        document.addEventListener('click', (ev) => {
          if (!themePopup) return;
          if (themePopup.getAttribute('aria-hidden') === 'false') {
            const path = ev.composedPath ? ev.composedPath() : (ev.path || []);
            if (!path.includes(themePopup) && ev.target !== themeBtn) closeThemePopup();
          }
        });
        // escape closes
        document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') closeThemePopup(); });
      }

      if (exampleSearch) {
        exampleSearch.addEventListener('input', () => {
          fillExamples();
        });
        exampleSearch.addEventListener('focus', () => {
          ensureExamplesLoaded().catch(() => {});
        }, { once: true });
      }
      if (examplesList) {
        examplesList.addEventListener('pointerenter', () => {
          ensureExamplesLoaded().catch(() => {});
        }, { once: true });
      }

      function setStatus(message, type = "loading") {
        statusEl.className = `status ${type}`;
        statusEl.innerHTML = `<div class="status-indicator"></div>${message}`;
      }

      let logLines = [];
      let maxLogLines = 100;
      let fadeTimeout = 8000; // 8 seconds before starting to fade
      let isFocused = false;

      // Enhanced logging with fading and auto-scroll. Lines are queued and
      // appended in one DocumentFragment per animation frame: a program that
      // prints thousands of lines costs one layout/scroll per frame instead
      // of one per line. Fading is handled by the periodic interval below.
      // type: 'output' | 'error' | 'warn' | 'system' (default: 'output')
      let pendingLogLines = [];
      let logFlushScheduled = false;

      // Matches the "line:col: " prefix RuntimeError.Error() and go/parser
      // both produce (see interp.RuntimeError, interp/evaluator.go's
      // evalExpr/evalStmt location tagging). Used to make error log lines
      // clickable — jumping straight to the failing source line instead of
      // leaving the user to search for it by eye.
      const ERROR_LOCATION_RE = /(\d+):(\d+):\s/;

      function logMessage(message, type, loc) {
        if (!loc && type === 'error') {
          const m = ERROR_LOCATION_RE.exec(String(message));
          if (m) loc = { line: parseInt(m[1], 10), col: parseInt(m[2], 10) };
        }
        pendingLogLines.push({ message: String(message), type: type || 'output', loc: loc || null });
        scheduleLogFlush();
        if (loc) highlightErrorLine(loc.line);
        postToHost({ type: 'nanogo:output', text: String(message), kind: type || 'output' });
      }

      // requestAnimationFrame is throttled or fully paused while the document
      // is hidden (e.g. the user switched tabs mid-run), which would make
      // output appear frozen until they tab back — fall back to a plain
      // timer in that case so console output keeps flowing regardless.
      function scheduleLogFlush() {
        if (logFlushScheduled) return;
        logFlushScheduled = true;
        if (typeof document !== 'undefined' && document.hidden) {
          setTimeout(flushLogQueue, 50);
        } else {
          requestAnimationFrame(flushLogQueue);
        }
      }

      document.addEventListener('visibilitychange', () => {
        if (!document.hidden && pendingLogLines.length > 0) flushLogQueue();
      });

      function flushLogQueue() {
        logFlushScheduled = false;
        if (pendingLogLines.length === 0) return;
        const batch = pendingLogLines;
        pendingLogLines = [];

        // Only the tail can stay visible anyway — skip DOM work for the rest.
        const start = Math.max(0, batch.length - maxLogLines);
        const timestamp = new Date().toLocaleTimeString();
        const fragment = document.createDocumentFragment();
        const now = Date.now();
        for (let i = start; i < batch.length; i++) {
          const { message, type, loc } = batch[i];
          const lineElement = document.createElement('div');
          lineElement.className = 'log-line log-' + type;
          lineElement.textContent = `[${timestamp}] ${message}`;
          if (loc) {
            // A data attribute, not a class: the fading logic below
            // periodically reassigns this element's whole className (to
            // add/remove 'fading'/'old'), which would silently discard any
            // extra class added via classList.add. Attributes survive that.
            lineElement.dataset.line = loc.line;
            lineElement.dataset.col = loc.col;
            lineElement.title = 'Jump to line ' + loc.line;
          }
          fragment.appendChild(lineElement);
          logLines.push({ text: lineElement.textContent, element: lineElement, timestamp: now, type });
        }
        logEl.appendChild(fragment);

        // Remove old lines if too many
        if (logLines.length > maxLogLines) {
          const removed = logLines.splice(0, logLines.length - maxLogLines);
          for (const oldLine of removed) {
            if (oldLine.element && oldLine.element.parentNode) {
              oldLine.element.parentNode.removeChild(oldLine.element);
            }
          }
        }

        logEl.scrollTop = logEl.scrollHeight;
      }

      function updateLogLinesFading() {
        if (isFocused) return;

        const now = Date.now();
        logLines.forEach(line => {
          if (!line.element) return;
          
          const typeClass = 'log-' + (line.type || 'output');
          const age = now - line.timestamp;
          if (age > fadeTimeout * 2) {
            line.element.className = 'log-line ' + typeClass + ' old';
          } else if (age > fadeTimeout) {
            line.element.className = 'log-line ' + typeClass + ' fading';
          } else {
            line.element.className = 'log-line ' + typeClass;
          }
        });
      }

      function clearLog() {
        logEl.innerHTML = "";
        logLines = [];
        if (execTimeEl) execTimeEl.textContent = '';
        
        // Add initial message
        const initialLine = document.createElement('div');
        initialLine.className = 'log-line log-system';
        initialLine.textContent = "🎯 Output cleared. Ready for new execution!";
        logEl.appendChild(initialLine);
        
        logLines.push({
          text: "🎯 Output cleared. Ready for new execution!",
          element: initialLine,
          timestamp: Date.now(),
          type: 'system'
        });

        // Force scroll to bottom after clearing
        setTimeout(() => {
          logEl.scrollTop = logEl.scrollHeight;
        }, 50);

        const c = document.getElementById('life');
        const ctx = c.getContext('2d');
        ctx.clearRect(0, 0, c.width, c.height);
        document.getElementById('output').innerHTML = '<p style="color: var(--text-muted); font-style: italic;">Output will appear here...</p>';
      }

      // Focus management for console
      logEl.addEventListener('focus', () => {
        isFocused = true;
        logEl.classList.add('focused');
        // Restore all lines to full opacity (preserve type class)
        logLines.forEach(line => {
          if (line.element) {
            line.element.className = 'log-line log-' + (line.type || 'output');
          }
        });
      });

      logEl.addEventListener('blur', () => {
        isFocused = false;
        logEl.classList.remove('focused');
        // Restart fading process
        setTimeout(updateLogLinesFading, 100);
      });

      // Update fading periodically
      setInterval(() => {
        if (!isFocused) {
          updateLogLinesFading();
        }
      }, 1000);
      clearBtn.onclick = clearLog;

      const labPanel = document.getElementById("labPanel");
      const aiPanel = document.getElementById("aiPanel");
      const symbolPanel = document.getElementById("symbolPanel");
      const OUTPUT_PANELS = { canvas: canvasPanel, dom: domPanel, lab: labPanel, ai: aiPanel, symbol: symbolPanel, inspector: inspectorPanel, console: consolePanel };

      function showOutputPanel(type) {
        const key = OUTPUT_PANELS[type] ? type : 'console';
        Object.keys(OUTPUT_PANELS).forEach(k => {
          OUTPUT_PANELS[k].classList.toggle('active', k === key);
        });
        outputTabs.forEach(tab => {
          const active = tab.dataset.output === key;
          tab.classList.toggle('active', active);
          tab.setAttribute('aria-selected', active ? 'true' : 'false');
        });
      }

      outputTabs.forEach(tab => {
        tab.addEventListener('click', () => showOutputPanel(tab.dataset.output));
      });

      // Cache the logical cell state, then paint only the newest complete
      // state on requestAnimationFrame. This preserves the order of animated
      // worker frames even when several arrive before the browser can paint.
      let _lifeCanvas = null;
      let _lifeCtx = null;
      function _getLifeCtx() {
        if (!_lifeCanvas) {
          _lifeCanvas = document.getElementById('life');
        }
        if (!_lifeCtx && _lifeCanvas) {
          try {
            _lifeCtx = _lifeCanvas.getContext('2d', { alpha: false });
          } catch (e) {
            _lifeCtx = _lifeCanvas.getContext('2d');
          }
        }
        return _lifeCtx;
      }
      let _canvasGridW = 0;
      let _canvasGridH = 0;
      let _canvasCells = new Uint8Array(0);
      let _canvasRenderScheduled = false;
      // Keep level 1 as the established green for binary demos. Palette-aware
      // programs may use levels 2..7 without changing their rendering path.
      const CANVAS_PALETTE = ['#080d13', '#10b981', '#0ea5e9', '#2563eb', '#7c3aed', '#ec4899', '#f97316', '#facc15'];
      // Fallback frame interval (≈ 60 fps) used only when
      // requestAnimationFrame (rAF) is unavailable in this context (very old
      // browsers / certain worker scopes). Real browsers will hit the rAF
      // branch below.
      const FRAME_DELAY_MS = 16;
      function _flushCells() {
        _canvasRenderScheduled = false;
        const ctx = _getLifeCtx();
        if (!ctx || _canvasGridW <= 0 || _canvasGridH <= 0) return;
        const cs = parseInt(scaleEl.value, 10) || 8;
        ctx.fillStyle = CANVAS_PALETTE[0];
        ctx.fillRect(0, 0, _canvasGridW * cs, _canvasGridH * cs);
        for (let level = 1; level < CANVAS_PALETTE.length; level++) {
          ctx.fillStyle = CANVAS_PALETTE[level];
          for (let i = 0; i < _canvasCells.length; i++) {
            if (_canvasCells[i] === level) {
              const x = i % _canvasGridW;
              const y = (i / _canvasGridW) | 0;
              ctx.fillRect(x * cs, y * cs, cs, cs);
            }
          }
        }
      }
      function _scheduleCanvasRender() {
        if (_canvasRenderScheduled) return;
        _canvasRenderScheduled = true;
        const useRaf = typeof requestAnimationFrame === 'function' && !(typeof document !== 'undefined' && document.hidden);
        (useRaf ? requestAnimationFrame : (cb) => setTimeout(cb, FRAME_DELAY_MS))(_flushCells);
      }
      function resizeCanvasGrid(w, h) {
        _canvasGridW = Math.max(0, w | 0);
        _canvasGridH = Math.max(0, h | 0);
        _canvasCells = new Uint8Array(_canvasGridW * _canvasGridH);
      }
      function setCanvasCell(x, y, level) {
        x |= 0; y |= 0;
        if (x < 0 || y < 0 || x >= _canvasGridW || y >= _canvasGridH) return;
        level = Number(level) | 0;
        _canvasCells[y * _canvasGridW + x] = level < 0 ? 0 : (level >= CANVAS_PALETTE.length ? CANVAS_PALETTE.length - 1 : level);
      }
      function applyCanvasFrame(data) {
        // Decode compact `x,y,level;...` without allocating one array/object
        // per cell. The worker already guarantees this is a whole frame.
        let i = 0;
        while (i < data.length) {
          let x = 0, y = 0, sign = 1;
          if (data.charCodeAt(i) === 45) { sign = -1; i++; }
          while (i < data.length) {
            const c = data.charCodeAt(i);
            if (c < 48 || c > 57) break;
            x = x * 10 + c - 48; i++;
          }
          x *= sign;
          if (data.charCodeAt(i++) !== 44) break;
          sign = 1;
          if (data.charCodeAt(i) === 45) { sign = -1; i++; }
          while (i < data.length) {
            const c = data.charCodeAt(i);
            if (c < 48 || c > 57) break;
            y = y * 10 + c - 48; i++;
          }
          y *= sign;
          if (data.charCodeAt(i++) !== 44) break;
          const level = data.charCodeAt(i++) - 48;
          while (i < data.length && data.charCodeAt(i) !== 59) i++;
          if (i < data.length) i++;
          setCanvasCell(x, y, level);
        }
        _scheduleCanvasRender();
      }
      function drawCell(x, y, alive) {
        setCanvasCell(x, y, alive ? 1 : 0);
        _scheduleCanvasRender();
      }

      function startWasmWorker() {
        if (worker) return;
        
        if (location.protocol === 'file:') {
          setStatus("Error: Must run on HTTP server, not file://", "error");
          logMessage("ERROR: WebAssembly requires HTTP/HTTPS protocol.", 'error');
          logMessage("Start a local server: python3 -m http.server 8080", 'system');
          return;
        }

        worker = new Worker('wasm_worker.js?9');
        worker.onmessage = (ev) => {
          const m = ev.data;
          if (!m || !m.type) return;
          if (m.type === 'batch') {
            // High-frequency messages (log/canvas) arrive coalesced from the
            // worker; unpack in order through the same dispatcher.
            for (const item of m.items) handleWorkerMessage(item);
            return;
          }
          handleWorkerMessage(m);
        };
        worker.onerror = (e) => {
          setStatus('Worker error', 'error');
          logMessage('❌ Worker error: ' + e.message, 'error');
        };
        worker.postMessage({ type: 'init' });
      }

      function finishRun(elapsed, stats) {
        const timeStr = elapsed != null ? ' (' + elapsed + 'ms)' : '';
        logMessage('✅ Execution finished' + timeStr, 'system');
        if (execTimeEl) {
          let label = elapsed != null ? elapsed + 'ms' : '';
          if (stats && stats.steps) label += (label ? ' · ' : '') + formatSteps(stats.steps) + ' steps';
          execTimeEl.textContent = label;
        }
        updateRunStats(elapsed, stats);
        lastRuntimeSource = activeRunSource;
        lastRuntimeVariables = (stats && stats.variables) || [];
        if (selectedSymbol) renderSelectedSymbol();
        if (stats && stats.trace) {
          applyBreakpointHits(stats.trace);
          renderTrace(stats.trace, stats.traceCap);
          document.dispatchEvent(new CustomEvent('nanogo:trace', { detail: { events: stats.trace, cap: stats.traceCap } }));
        } else {
          applyBreakpointHits([]);
        }
        if (stats && stats.profile) applyHeatmap(stats.profile);
        setStatus(stats && stats.error ? 'Execution failed' : 'Ready', stats && stats.error ? 'error' : 'ready');
        workspaceDirty = false;
        renderWorkspaceTabs();
        if (workspaceRunBtn) { workspaceRunBtn.disabled = false; workspaceRunBtn.textContent = '▶ Project'; }
      }

      function handleTestResult(result, error, scope) {
        if (testBtn) { testBtn.disabled = false; testBtn.textContent = '🧪 Test'; }
        if (error) {
          logMessage('🧪 ' + (scope || 'Test') + ' error: ' + error, 'error');
          return;
        }
        if (!result || !result.total) {
          logMessage('🧪 No matching TestXxx functions found', 'warn');
          return;
        }
        if (result.passed) {
          const skipped = (result.results || []).filter(test => test.skipped).length;
          logMessage('🧪 PASS: ' + result.total + ' test(s)' + (skipped ? ', ' + skipped + ' skipped' : '') + ' ✅', 'system');
          return;
        }
        logMessage('🧪 FAIL: ' + result.failed + ' of ' + result.total + ' test(s) failed', 'error');
        (result.results || []).filter(test => !test.passed).forEach(test => {
          logMessage('  ' + (test.name || 'Test') + ' at ' + test.line + ':' + test.column + ' — ' + test.category, 'error');
          (test.messages || []).forEach(message => logMessage('    ' + message, 'error'));
        });
      }

      function handleWorkerMessage(m) {
          switch (m.type) {
            case 'ready':
              setStatus('Ready', 'ready');
              runBtn.disabled = false;
              wasmCapabilities = m.capabilities || {};
              if (astBtn && wasmCapabilities.hasAst === false) astBtn.disabled = true;
              if (callGraphBtn && wasmCapabilities.hasCallGraph === false) callGraphBtn.disabled = true;
              if (benchBtn && wasmCapabilities.hasBench === false) benchBtn.disabled = true;
              if (heatmapToggle && wasmCapabilities.hasProfile === false) heatmapToggle.disabled = true;
              if (workspaceCheckBtn && wasmCapabilities.hasModuleCheck === false) workspaceCheckBtn.disabled = true;
              if (workspaceInspectBtn && wasmCapabilities.hasModuleCheck === false) workspaceInspectBtn.disabled = true;
              if (workspaceRunBtn && wasmCapabilities.hasWorkspace === false) workspaceRunBtn.disabled = true;
              if (workspaceMetaEl && wasmCapabilities.sdk) workspaceMetaEl.textContent = wasmCapabilities.sdk;
              logMessage('🎉 WebAssembly loaded and ready!', 'system');
              if (exampleCountBadgeEl) exampleCountBadgeEl.textContent = String(EXAMPLE_NAMES.length);
              fillExamples();
              introduceExamplesDrawer();
              loadCodeFromURL().then(() => {
                setupEmbedPopout();
                postToHost({ type: 'nanogo:ready' });
                if (wantAutorun) runCode();
              });
              break;
            case 'log':
              logMessage(String(m.text), 'output');
              break;
            case 'warn':
              logMessage('⚠️ WARN: ' + String(m.text), 'warn');
              break;
            case 'error':
              logMessage('❌ ERROR: ' + String(m.text), 'error');
              setStatus('Runtime error', 'error');
              if (execTimeEl) execTimeEl.textContent = '';
              break;
            case 'canvas-size': {
              autoSwitchOutputPanel('canvas');
              const w = Number(m.w), h = Number(m.h);
              const scale = parseInt(scaleEl.value, 10);
              const canvas = document.getElementById('life');
              resizeCanvasGrid(w, h);
              canvas.width = w * scale;
              canvas.height = h * scale;
              break;
            }
            case 'canvas-set': {
              autoSwitchOutputPanel('canvas');
              const cx = Number(m.x), cy = Number(m.y), alive = !!m.alive;
              drawCell(cx, cy, alive);
              break;
            }
            case 'canvas-set-level':
              autoSwitchOutputPanel('canvas');
              setCanvasCell(Number(m.x), Number(m.y), Number(m.level));
              _scheduleCanvasRender();
              break;
            case 'canvas-frame': {
              autoSwitchOutputPanel('canvas');
              // The WASM worker encodes a whole frame as `x,y,alive;...` so
              // Go crosses into JavaScript once per frame instead of once per
              // cell. Decode directly into the existing rAF drawing queue.
              applyCanvasFrame(String(m.data || ''));
              break;
            }
            case 'canvas-flush':
              autoSwitchOutputPanel('canvas');
              break;
            case 'dom-setinner': {
              autoSwitchOutputPanel('dom');
              const el = document.getElementById(m.id);
              if (el) el.innerHTML = m.html;
              logMessage('📄 HTML template rendered', 'system');
              break;
            }
            case 'dom-setvalue': {
              autoSwitchOutputPanel('dom');
              const elv = document.getElementById(m.id);
              if (elv) elv.value = m.value;
              break;
            }
            case 'dom-addclass': {
              autoSwitchOutputPanel('dom');
              const ea = document.getElementById(m.id);
              if (ea) ea.classList.add(m.class);
              break;
            }
            case 'dom-removeclass': {
              autoSwitchOutputPanel('dom');
              const er = document.getElementById(m.id);
              if (er) er.classList.remove(m.class);
              break;
            }
            case 'open-window': {
              try { window.open(m.url, '_blank') } catch (e) { console.warn(e) }
              break;
            }
            case 'alert': {
              try { window.alert(m.text) } catch (e) { console.warn(e) }
              break;
            }
            case 'format-result':
              if (m.error) {
                logMessage('✨ Format error: ' + m.error, 'error');
              } else if (m.source != null) {
                setSource(m.source);
                logMessage('✨ Code formatted', 'system');
              }
              break;
            case 'vet-result':
              if (m.error) {
                logMessage('🔍 Vet error: ' + m.error, 'error');
              } else if (m.issues && m.issues.length === 0) {
                logMessage('🔍 Vet: no issues found ✅', 'system');
              } else if (m.issues) {
                logMessage('🔍 Vet found ' + m.issues.length + ' issue(s):', 'warn');
                m.issues.forEach(iss => logMessage('  ' + iss.line + ':' + iss.column + ': ' + iss.message, 'warn'));
              }
              break;
            case 'workspace-check-result':
              if (workspaceCheckBtn) { workspaceCheckBtn.disabled = false; workspaceCheckBtn.textContent = '✓ Check'; }
              if (workspaceInspectBtn) { workspaceInspectBtn.disabled = false; workspaceInspectBtn.textContent = 'Check imports'; }
              showWorkspaceResult(m.result, m.error);
              if (m.error) logMessage('📁 Workspace check: ' + m.error, 'error');
              else logMessage('📁 Workspace checked: ' + ((m.result && m.result.workspace && m.result.workspace.packages) || []).length + ' package(s) ✅', 'system');
              break;
            case 'workspace-done':
              if (m.error) {
                if (workspaceRunBtn) { workspaceRunBtn.disabled = false; workspaceRunBtn.textContent = '▶ Project'; }
                setStatus('Workspace error', 'error');
                logMessage('📁 Workspace run error: ' + m.error, 'error');
              } else {
                if (m.stats && m.stats.workspace) showWorkspaceResult({ workspace: m.stats.workspace });
                finishRun(m.elapsed, m.stats);
              }
              break;
            case 'test-result':
              handleTestResult(m.result, m.error, 'Test');
              break;
            case 'workspace-test-result':
              handleTestResult(m.result, m.error, 'Workspace test');
              break;
            case 'ast-result':
              if (astBtn) { astBtn.disabled = false; astBtn.textContent = 'Parse current code'; }
              if (m.error) {
                if (astMetaEl) astMetaEl.textContent = '';
                if (astTreeEl) astTreeEl.innerHTML = '<div class="inspector-error">' + escapeHtml(m.error) + '</div>';
                logMessage('🌳 AST error: ' + m.error, 'error');
              } else if (m.result) {
                renderAst(m.result);
              }
              break;
            case 'callgraph-result':
              const analyzedSource = callGraphRequestSources.shift() || getSource();
              if (callGraphBtn) { callGraphBtn.disabled = false; callGraphBtn.textContent = 'Analyze functions'; }
              if (m.error) {
                if (callGraphMetaEl) callGraphMetaEl.textContent = '';
                if (callGraphListEl) callGraphListEl.innerHTML = '<div class="inspector-error">' + escapeHtml(m.error) + '</div>';
                if (selectedSymbol && selectedSymbol.source === analyzedSource && symbolDetailsEl) {
                  symbolDetailsEl.innerHTML = '<div class="inspector-error">' + escapeHtml(m.error) + '</div>';
                }
                logMessage('📞 Function analysis error: ' + m.error, 'error');
              } else if (m.result) {
                lastCallGraphSource = analyzedSource;
                symbolAnalysisPendingSource = '';
                renderCallGraph(m.result);
                if (selectedSymbol && selectedSymbol.source === analyzedSource) renderSelectedSymbol();
                document.dispatchEvent(new CustomEvent('nanogo:callgraph', { detail: m.result }));
              }
              break;
            case 'bench-result':
              if (benchBtn) { benchBtn.disabled = false; benchBtn.textContent = 'Run benchmark'; }
              setStatus('Ready', 'ready');
              if (m.error) {
                if (benchResultsEl) benchResultsEl.innerHTML = '<div class="inspector-error">' + escapeHtml(m.error) + '</div>';
                logMessage('📊 Benchmark error: ' + m.error, 'error');
              } else if (m.result) {
                renderBench(m.result);
                logMessage('📊 Benchmark: ' + m.result.iterations + ' run(s), avg ' + m.result.avgMs.toFixed(2) + 'ms, ' + formatSteps(m.result.stepsPerOp) + ' steps/op', 'system');
                if (m.result.profile) applyHeatmap(m.result.profile);
              }
              break;
            case 'debug-pause': {
              let info = null;
              try { info = JSON.parse(m.json); } catch (e) { /* ignore malformed payload */ }
              if (info) document.dispatchEvent(new CustomEvent('nanogo:debug-pause', { detail: info }));
              break;
            }
            case 'debug-done': {
              let result = null;
              try { result = JSON.parse(m.json); } catch (e) { /* ignore malformed payload */ }
              if (!result && m.error) result = { error: m.error };
              document.dispatchEvent(new CustomEvent('nanogo:debug-done', { detail: result || {} }));
              break;
            }
            case 'debug-command-result':
              document.dispatchEvent(new CustomEvent('nanogo:debug-command-result', { detail: m }));
              break;
            case 'done': {
              const elapsed = m.elapsed != null ? m.elapsed : null;
              const stats = m.stats || null;
              finishRun(elapsed, stats);
              postToHost({ type: 'nanogo:done', elapsed, stats });
              break;
            }
            default:
              console.log('worker:', m);
          }
      }

      function runCode() {
        try {
          if (!worker) throw new Error('worker not initialized');
          if (isWorkspaceProject()) {
            runWorkspace();
            return;
          }
          clearErrorHighlight();
          autoSwitchedOutputThisRun = false;

          // Preselect the output panel from the example's static metadata, if
          // any -- this is just a starting guess, shown before the program
          // has run a single line. autoSwitchOutputPanel (see handleWorkerMessage)
          // overrides it the moment the program actually draws to the canvas
          // or writes HTML, which is what makes this work for hand-written
          // code (no metadata at all) and for an example whose edited code no
          // longer matches its original classification.
          if (currentExample && EXAMPLE_CONFIG[currentExample]) {
            showOutputPanel(EXAMPLE_CONFIG[currentExample].output);
          }
          
          const scale = parseInt(scaleEl.value, 10);
          worker.postMessage({ type: 'set-scale', scale: scale });
          
          const c = document.getElementById('life');
          const ctx = c.getContext('2d');
          ctx.clearRect(0, 0, c.width, c.height);
          
          if (execTimeEl) execTimeEl.textContent = '';
          const source = getSource();
          activeRunSource = source;
          localStorage.setItem("nanogo_last", source);
          logMessage('🚀 Running…', 'system');
          setStatus('Running code...', 'loading');
          const mode = modeSelect.value || 'stream';
          const breakpoints = breakpointLineNumbers();
          const wantTrace = !!(traceToggle && traceToggle.checked) || breakpoints.length > 0;
          const wantProfile = !!(heatmapToggle && heatmapToggle.checked);
          breakpointHits.clear();
          refreshBreakpointMarkers();
          clearHeatmap();
          const t0 = performance.now();
          worker.postMessage({ type: 'run', source, mode: mode, trace: wantTrace, profile: wantProfile, breakpoints: breakpoints, t0 });
        } catch (err) {
          setStatus('Runtime error', 'error');
          logMessage('❌ RUNTIME ERROR: ' + err.message, 'error');
        }
      }

      runBtn.onclick = runCode;
      if (workspaceRunBtn) workspaceRunBtn.onclick = runWorkspace;
      if (workspaceCheckBtn) workspaceCheckBtn.onclick = requestWorkspaceCheck;
      if (workspaceInspectBtn) workspaceInspectBtn.onclick = requestWorkspaceCheck;
      if (workspaceAddBtn) workspaceAddBtn.onclick = () => {
        const suggested = 'helper.go';
        const filePath = window.prompt('New Go file path', suggested);
        if (!filePath) return;
        if (!addWorkspaceFile(filePath, 'package main\n\nfunc helper() {\n}\n')) {
          logMessage('📁 Invalid file path. Use a .go file below the workspace root.', 'warn');
        } else {
          logMessage('📁 Added ' + filePath, 'system');
        }
      };

      function runDebug() {
        if (traceToggle) traceToggle.checked = true;
        if (heatmapToggle) heatmapToggle.checked = true;
        showOutputPanel('lab');
        runCode();
        // runCode() just reset autoSwitchedOutputThisRun so a plain run can
        // react to what the program draws; a debug run is explicitly about
        // watching the Lab panel's trace, so re-arm the "already switched"
        // flag right after so a canvas/dom message during this run can't
        // yank focus away from it. Safe because the worker's response
        // messages only arrive on a later event-loop turn than this line.
        autoSwitchedOutputThisRun = true;
      }

      // ---------------- Live debug (real pause/step, see web/live_debug.js) ----------------
      // Unlike runDebug() above — which records a whole deterministic run and
      // replays it afterward — this starts the guest program on the worker's
      // own background goroutine and lets it actually park mid-statement at a
      // breakpoint or step target. debugCommand() sends the small control
      // messages (continue/step-into/step-over/step-out/pause/stop) that
      // resume it; see cmd/wasm's nanoGoDebug* exports and wasm_worker.js's
      // 'debug-start'/'debug-command' handlers for the other side.
      function startLiveDebug() {
        if (!worker) return;
        clearErrorHighlight();
        showOutputPanel('lab');
        // See runDebug()'s matching comment: a live-debug session is about
        // watching Lab (call stack, locals), so pin the output tab there --
        // a canvas/dom message from the paused program must not steal focus.
        autoSwitchedOutputThisRun = true;
        const source = getSource();
        activeRunSource = source;
        const breakpoints = breakpointLineNumbers();
        worker.postMessage({ type: 'debug-start', source: source, breakpoints: breakpoints });
      }
      function debugCommand(command, extra) {
        if (!worker) return;
        worker.postMessage(Object.assign({ type: 'debug-command', command: command }, extra || {}));
      }

      // ---------------- Execution inspector ----------------

      function escapeHtml(s) {
        return String(s).replace(/[&<>"']/g, c => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
      }

      function formatSteps(n) {
        if (n == null) return '–';
        if (n >= 1e6) return (n / 1e6).toFixed(2) + 'M';
        if (n >= 1e3) return (n / 1e3).toFixed(1) + 'k';
        return String(n);
      }

      function updateRunStats(wallMs, stats) {
        if (statWallEl) statWallEl.textContent = wallMs != null ? wallMs + 'ms' : '–';
        if (statInterpEl) statInterpEl.textContent = stats && stats.elapsedMs != null ? stats.elapsedMs.toFixed(2) + 'ms' : '–';
        if (statStepsEl) statStepsEl.textContent = stats ? formatSteps(stats.steps) : '–';
        if (statStepsRateEl) {
          statStepsRateEl.textContent = (stats && stats.steps && stats.elapsedMs > 0)
            ? formatSteps(Math.round(stats.steps / stats.elapsedMs))
            : '–';
        }
      }

      function jumpToSource(line, col) {
        if (!editor || !line) return;
        editor.setCursor({ line: line - 1, ch: Math.max(0, (col || 1) - 1) });
        editor.focus();
      }

      // Persistent (until the next run or edit) highlight on the line a
      // run's error was reported at — an ambient "look here" that doesn't
      // require the user to have noticed/clicked the console line at all.
      // Uses a line handle rather than a bare index so it keeps tracking the
      // right line even if the error is left in place while editing above it.
      let errorLineHandle = null;
      function clearErrorHighlight() {
        if (editor && errorLineHandle) editor.removeLineClass(errorLineHandle, 'wrap', 'cm-error-line');
        errorLineHandle = null;
      }
      function highlightErrorLine(line) {
        clearErrorHighlight();
        if (!editor || !line) return;
        const idx = Math.min(Math.max(line - 1, 0), editor.lineCount() - 1);
        errorLineHandle = editor.getLineHandle(idx);
        // 'wrap' (not 'background'): with lineWrapping on, the background
        // div this editor generates measures zero height, so a background
        // color on it never paints anything. 'wrap' targets the line's
        // actual content wrapper, which always has real height.
        if (errorLineHandle) editor.addLineClass(errorLineHandle, 'wrap', 'cm-error-line');
      }

      // Line-execution heatmap: darker background = executed more often
      // (see interp.LineProfile / stats.profile). Buckets are log-scaled
      // rather than linear — a hot loop can run thousands of times more
      // than a cold one-off line, and a linear scale would make everything
      // but that one loop look equally "cold" by comparison.
      const HEATMAP_BUCKETS = 10;
      let heatmapLineHandles = [];

      function heatmapBucket(count, logMax) {
        if (logMax <= 0) return HEATMAP_BUCKETS;
        return Math.max(1, Math.min(HEATMAP_BUCKETS, Math.ceil(Math.log(count + 1) / logMax * HEATMAP_BUCKETS)));
      }

      function clearHeatmap() {
        if (editor) {
          heatmapLineHandles.forEach(({ handle, cls }) => editor.removeLineClass(handle, 'wrap', cls));
        }
        heatmapLineHandles = [];
        if (heatmapListEl) heatmapListEl.innerHTML = '';
        if (heatmapMetaEl) heatmapMetaEl.textContent = '';
      }

      function applyHeatmap(hits) {
        clearHeatmap();
        if (!editor || !hits || hits.length === 0) {
          if (heatmapListEl) heatmapListEl.innerHTML = '<p class="inspector-note">No hits recorded. Check "Record line heatmap" above, then Run or Benchmark again.</p>';
          return;
        }
        const maxCount = hits.reduce((m, h) => Math.max(m, h.count), 1);
        const logMax = Math.log(maxCount + 1);

        hits.forEach(h => {
          const idx = Math.min(Math.max(h.line - 1, 0), editor.lineCount() - 1);
          const handle = editor.getLineHandle(idx);
          if (!handle) return;
          const cls = 'cm-heat-' + heatmapBucket(h.count, logMax);
          editor.addLineClass(handle, 'wrap', cls);
          heatmapLineHandles.push({ handle, cls });
        });

        if (heatmapMetaEl) {
          const totalHits = hits.reduce((s, h) => s + h.count, 0);
          heatmapMetaEl.textContent = hits.length + ' line(s) · ' + formatSteps(totalHits) + ' total hits · hottest ' + formatSteps(maxCount) + '×';
        }
        if (heatmapSectionEl) heatmapSectionEl.open = true;

        const HEATMAP_LIST_LIMIT = 30;
        const sorted = hits.slice().sort((a, b) => b.count - a.count).slice(0, HEATMAP_LIST_LIMIT);
        const rows = sorted.map(h => {
          const bucket = heatmapBucket(h.count, logMax);
          return '<tr><td><button class="trace-loc heatmap-loc" data-line="' + h.line + '">L' + h.line + '</button></td>' +
            '<td>' + formatSteps(h.count) + '</td>' +
            '<td><span class="heat-bar heat-bar-' + bucket + '"></span></td></tr>';
        }).join('');
        heatmapListEl.innerHTML =
          '<table class="inspector-table"><thead><tr><th>Line</th><th>Hits</th><th>Heat</th></tr></thead><tbody>' + rows + '</tbody></table>' +
          (hits.length > HEATMAP_LIST_LIMIT ? '<p class="inspector-note">Showing the ' + HEATMAP_LIST_LIMIT + ' hottest of ' + hits.length + ' executed lines.</p>' : '');
      }

      if (heatmapListEl) {
        heatmapListEl.addEventListener('click', (ev) => {
          const btn = ev.target.closest('.heatmap-loc');
          if (btn) jumpToSource(parseInt(btn.dataset.line, 10), 1);
        });
      }
      if (heatmapClearBtn) heatmapClearBtn.addEventListener('click', clearHeatmap);

      if (logEl) {
        logEl.addEventListener('click', (ev) => {
          const line = ev.target.closest('.log-line[data-line]');
          if (line) jumpToSource(parseInt(line.dataset.line, 10), parseInt(line.dataset.col, 10));
        });
      }

      // AST tree: nodes below the eagerly-rendered top levels are built
      // lazily on first expansion, so a large file stays snappy.
      const AST_EAGER_DEPTH = 3;

      function astLabelEl(node) {
        const label = document.createElement('span');
        label.className = 'ast-label';
        const typeEl = document.createElement('span');
        typeEl.className = 'ast-type';
        typeEl.textContent = node.type;
        label.appendChild(typeEl);
        if (node.label) {
          const nameEl = document.createElement('span');
          nameEl.className = 'ast-name';
          nameEl.textContent = node.label;
          label.appendChild(nameEl);
        }
        if (node.line) {
          const posEl = document.createElement('span');
          posEl.className = 'ast-pos';
          posEl.textContent = ':' + node.line;
          posEl.title = 'Jump to line ' + node.line;
          posEl.addEventListener('click', (ev) => {
            ev.preventDefault();
            ev.stopPropagation();
            jumpToSource(node.line, node.col);
          });
          label.appendChild(posEl);
        }
        return label;
      }

      function buildAstNode(node, depth) {
        const hasChildren = node.children && node.children.length > 0;
        if (!hasChildren) {
          const leaf = document.createElement('div');
          leaf.className = 'ast-leaf';
          leaf.appendChild(astLabelEl(node));
          return leaf;
        }
        const det = document.createElement('details');
        det.className = 'ast-node';
        const sum = document.createElement('summary');
        sum.appendChild(astLabelEl(node));
        const count = document.createElement('span');
        count.className = 'ast-count';
        count.textContent = node.children.length;
        sum.appendChild(count);
        det.appendChild(sum);
        const box = document.createElement('div');
        box.className = 'ast-children';
        det.appendChild(box);

        let built = false;
        const build = () => {
          if (built) return;
          built = true;
          for (const child of node.children) box.appendChild(buildAstNode(child, depth + 1));
        };
        if (depth < AST_EAGER_DEPTH) {
          det.open = true;
          build();
        } else {
          det.addEventListener('toggle', () => { if (det.open) build(); });
        }
        return det;
      }

      function renderAst(result) {
        if (!astTreeEl) return;
        if (astMetaEl) {
          astMetaEl.textContent = result.nodeCount + ' nodes · depth ' + result.maxDepth +
            ' · parse ' + (result.parseUs / 1000).toFixed(2) + 'ms' +
            (result.funcs && result.funcs.length ? ' · funcs: ' + result.funcs.join(', ') : '');
        }
        astTreeEl.innerHTML = '';
        if (result.tree) astTreeEl.appendChild(buildAstNode(result.tree, 0));
      }

      function requestAst() {
        if (!worker) { logMessage('Worker not ready', 'warn'); return; }
        if (astBtn) { astBtn.disabled = true; astBtn.textContent = 'Parsing…'; }
        worker.postMessage({ type: 'ast', source: getSource() });
      }

      // Functions / call graph: who calls whom. Resolution is best-effort
      // (see interp.AnalyzeCallGraph) — an unresolved call (e.g. fmt.Println,
      // or a method call the analyzer couldn't uniquely attribute to a type)
      // is still listed under "Calls" but renders as plain text, not a link.
      let lastCallGraphResult = null;
      let lastCallGraphSource = '';
      let symbolAnalysisPendingSource = '';
      const callGraphRequestSources = [];
      let selectedSymbol = null;
      let symbolMarks = [];
      let callGraphView = 'list';

      function clearSymbolMarks() {
        symbolMarks.forEach(mark => mark.clear());
        symbolMarks = [];
      }

      function markSymbolOccurrences(name, selectedLine, selectedColumn) {
        clearSymbolMarks();
        if (!editor || !name) return;
        const escaped = name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
        const pattern = new RegExp('(^|[^A-Za-z0-9_])(' + escaped + ')(?=$|[^A-Za-z0-9_])', 'g');
        for (let line = 0; line < editor.lineCount() && symbolMarks.length < 200; line++) {
          const text = editor.getLine(line);
          pattern.lastIndex = 0;
          let match;
          while ((match = pattern.exec(text)) !== null && symbolMarks.length < 200) {
            const start = match.index + match[1].length;
            const token = editor.getTokenAt({ line, ch: start + 1 });
            const tokenType = token && token.type ? token.type : '';
            if (!/(comment|string)/.test(tokenType)) {
              const selected = line + 1 === selectedLine && start + 1 === selectedColumn;
              symbolMarks.push(editor.markText(
                { line, ch: start }, { line, ch: start + name.length },
                { className: selected ? 'cm-symbol-selected' : 'cm-symbol-highlight' }
              ));
            }
            if (match[0].length === 0) pattern.lastIndex++;
          }
        }
      }

      function renderSymbolEmpty(message) {
        if (symbolPanelBadgeEl) symbolPanelBadgeEl.textContent = 'Editor';
        if (!symbolDetailsEl) return;
        symbolDetailsEl.innerHTML = '<div class="symbol-empty"><span class="symbol-empty-icon" aria-hidden="true">⌁</span>' +
          '<strong>Inspect code in place</strong><p>' + escapeHtml(message || 'Click a variable or function in the editor to see details.') + '</p></div>';
      }

      function invalidateSymbolSelection() {
        lastCallGraphSource = '';
        symbolAnalysisPendingSource = '';
        selectedSymbol = null;
        clearSymbolMarks();
        renderSymbolEmpty('The source changed. Click a symbol to inspect the updated code.');
      }

      function inspectEditorSymbolAt(pos) {
        if (!editor || !pos) return;
        const lineText = editor.getLine(pos.line) || '';
        const token = editor.getTokenAt({ line: pos.line, ch: Math.min(pos.ch + 1, lineText.length) });
        const name = token && String(token.string || '').trim();
        const tokenType = token && token.type ? token.type : '';
        if (!name || !/^[A-Za-z_][A-Za-z0-9_]*$/.test(name) || /(comment|string|number|keyword)/.test(tokenType)) return;

        const column = Math.max(1, (token.start || pos.ch) + 1);
        selectedSymbol = { name, line: pos.line + 1, column, source: getSource() };
        markSymbolOccurrences(name, selectedSymbol.line, column);
        showOutputPanel('symbol');
        renderSelectedSymbol();
      }

      function simpleSymbolName(name) {
        const parts = String(name || '').split('.');
        return parts[parts.length - 1];
      }

      function resolveSelectedFunction(selection) {
        if (!lastCallGraphResult || lastCallGraphSource !== selection.source) return null;
        const funcs = lastCallGraphResult.funcs || [];
        const byName = {};
        funcs.forEach(fn => { byName[fn.name] = fn; });

        // A call site is the strongest signal, especially when two receiver
        // types expose methods with the same short name.
        for (const owner of funcs) {
          for (const call of (owner.calls || [])) {
            if (call.line === selection.line && simpleSymbolName(call.name) === selection.name && call.resolved && byName[call.resolved]) {
              return byName[call.resolved];
            }
          }
        }
        const declarations = funcs.filter(fn => fn.line === selection.line && simpleSymbolName(fn.name) === selection.name);
        if (declarations.length === 1) return declarations[0];
        const exact = funcs.filter(fn => fn.name === selection.name);
        if (exact.length === 1) return exact[0];
        const short = funcs.filter(fn => simpleSymbolName(fn.name) === selection.name);
        return short.length === 1 ? short[0] : null;
      }

      function symbolJumpButton(label, line, extraClass) {
        if (!line) return '<span class="symbol-muted">' + escapeHtml(label) + '</span>';
        return '<button class="symbol-link ' + (extraClass || '') + '" data-jump-line="' + line + '">' + escapeHtml(label) + '</button>';
      }

      function renderFunctionSymbol(fn) {
        const funcs = (lastCallGraphResult && lastCallGraphResult.funcs) || [];
        const byName = {};
        funcs.forEach(item => { byName[item.name] = item; });
        const calls = (fn.calls || []).map(call => {
          const target = call.resolved && byName[call.resolved];
          const label = call.name + (target ? '' : ' · external');
          return '<li>' + symbolJumpButton(label, call.line, target ? 'resolved' : 'external') + '</li>';
        }).join('') || '<li class="symbol-muted">No function calls</li>';
        const callers = (fn.calledBy || []).map(name => {
          const caller = byName[name];
          let line = caller && caller.line;
          if (caller) {
            const callSite = (caller.calls || []).find(call => call.resolved === fn.name);
            if (callSite) line = callSite.line;
          }
          return '<li>' + symbolJumpButton(name, line, 'resolved') + '</li>';
        }).join('') || '<li class="symbol-muted">Entry point or no local caller</li>';

        if (symbolPanelBadgeEl) symbolPanelBadgeEl.textContent = 'Function';
        symbolDetailsEl.innerHTML =
          '<div class="symbol-heading"><div><span class="symbol-kind">Function</span><h2>' + escapeHtml(fn.name) + (fn.recursive ? ' <span title="Recursive">↺</span>' : '') + '</h2></div>' +
          symbolJumpButton('Definition · L' + fn.line, fn.line, 'definition') + '</div>' +
          '<div class="symbol-metrics"><span><strong>' + (fn.calledBy || []).length + '</strong> callers</span><span><strong>' + (fn.calls || []).length + '</strong> calls</span><span><strong>' + fn.complexity + '</strong> complexity</span><span><strong>' + fn.loc + '</strong> LOC</span></div>' +
          '<div class="symbol-grid"><section class="symbol-section"><h3>Called by</h3><ul>' + callers + '</ul></section>' +
          '<section class="symbol-section"><h3>Calls</h3><ul>' + calls + '</ul></section></div>';
      }

      function prettyRuntimeType(type) {
        if (!type || type === '<nil>') return 'nil';
        return String(type).replace(/^\*?interp\./, '');
      }

      function renderVariableSymbol(selection) {
        const funcs = (lastCallGraphResult && lastCallGraphResult.funcs) || [];
        const owner = funcs.find(fn => selection.line >= fn.line && selection.line < fn.line + Math.max(1, fn.loc || 1));
        const ownerName = owner ? simpleSymbolName(owner.name) : '';
        const allMatches = lastRuntimeVariables
          .filter(snapshot => snapshot.name === selection.name)
          .sort((a, b) => (b.sequence || 0) - (a.sequence || 0));
        // Prefer the lexical function containing the clicked token. Values
        // from same-named bindings in other functions stay visible below.
        const matches = ownerName
          ? allMatches.filter(snapshot => snapshot.function === ownerName).concat(allMatches.filter(snapshot => snapshot.function !== ownerName))
          : allMatches;
        const stale = !!lastRuntimeSource && lastRuntimeSource !== selection.source;
        if (symbolPanelBadgeEl) symbolPanelBadgeEl.textContent = matches.length ? 'Runtime value' : 'Variable';

        let valueBlock;
        if (matches.length) {
          const latest = matches[0];
          const location = latest.line ? symbolJumpButton((latest.file ? latest.file.split('/').pop() + ' · ' : '') + 'L' + latest.line, latest.line, 'definition') : '<span class="symbol-muted">parameter / runtime binding</span>';
          valueBlock = '<div class="symbol-value-card ' + (stale ? 'stale' : '') + '">' +
            '<div class="symbol-value-label">' + (stale ? 'Last value before source changed' : 'Last observed value') + '</div>' +
            '<pre>' + escapeHtml(latest.value) + '</pre>' +
            '<div class="symbol-value-meta"><span>' + escapeHtml(prettyRuntimeType(latest.type)) + '</span><span>' + escapeHtml(latest.function || 'program') + '</span><span>' + latest.writes + ' write' + (latest.writes === 1 ? '' : 's') + '</span>' + location + '</div></div>';
        } else {
          valueBlock = '<div class="symbol-no-value"><strong>No runtime value yet</strong><p>Run the program to capture the latest assignment for this variable.</p><button class="mini-btn" data-symbol-run>▶ Run and inspect</button></div>';
        }

        const scopes = matches.slice(1).map(snapshot => '<li><span><strong>' + escapeHtml(snapshot.function || 'program') + '</strong> · ' + escapeHtml(prettyRuntimeType(snapshot.type)) + '</span><code>' + escapeHtml(snapshot.value) + '</code></li>').join('');
        symbolDetailsEl.innerHTML = '<div class="symbol-heading"><div><span class="symbol-kind variable">Variable</span><h2>' + escapeHtml(selection.name) + '</h2></div>' +
          '<span class="symbol-location">Selected · L' + selection.line + ':' + selection.column + '</span></div>' + valueBlock +
          (scopes ? '<section class="symbol-section symbol-scopes"><h3>Other scopes</h3><ul>' + scopes + '</ul></section>' : '') +
          '<p class="symbol-footnote">Values are last-write snapshots from the most recent run; mutable fields are shown when they are assigned.</p>';
      }

      function renderSelectedSymbol() {
        if (!selectedSymbol || !symbolDetailsEl) return;
        if (lastCallGraphSource !== selectedSymbol.source) {
          symbolDetailsEl.innerHTML = '<div class="symbol-loading"><span class="status-indicator"></span><div><strong>Analyzing ' + escapeHtml(selectedSymbol.name) + '</strong><p>Resolving functions and runtime values…</p></div></div>';
          if (symbolAnalysisPendingSource !== selectedSymbol.source) {
            symbolAnalysisPendingSource = selectedSymbol.source;
            requestCallGraph();
          }
          return;
        }
        const fn = resolveSelectedFunction(selectedSymbol);
        if (fn) renderFunctionSymbol(fn);
        else renderVariableSymbol(selectedSymbol);
      }

      if (symbolDetailsEl) {
        symbolDetailsEl.addEventListener('click', event => {
          const jump = event.target.closest('[data-jump-line]');
          if (jump) {
            jumpToSource(Number(jump.dataset.jumpLine), 1);
            return;
          }
          if (event.target.closest('[data-symbol-run]')) runCode();
        });
      }

      function renderCallGraph(result) {
        lastCallGraphResult = result;
        renderCallGraphList(result);
        renderCallGraphDiagram(result);
      }

      function setCallGraphView(view) {
        callGraphView = view;
        if (callGraphListEl) callGraphListEl.hidden = view !== 'list';
        if (callGraphDiagramEl) callGraphDiagramEl.hidden = view !== 'diagram';
        if (callGraphViewSwitchEl) {
          callGraphViewSwitchEl.querySelectorAll('.view-switch-btn').forEach(btn => {
            btn.classList.toggle('active', btn.dataset.view === view);
          });
        }
      }
      if (callGraphViewSwitchEl) {
        callGraphViewSwitchEl.addEventListener('click', (ev) => {
          const btn = ev.target.closest('.view-switch-btn');
          if (!btn) return;
          setCallGraphView(btn.dataset.view);
        });
      }

      function renderCallGraphList(result) {
        if (!callGraphListEl) return;
        const funcs = result.funcs || [];
        if (callGraphMetaEl) {
          const totalCalls = funcs.reduce((sum, f) => sum + (f.calls ? f.calls.length : 0), 0);
          callGraphMetaEl.textContent = funcs.length + ' function(s) · ' + totalCalls + ' call site(s)';
        }
        callGraphListEl.innerHTML = '';
        if (funcs.length === 0) {
          callGraphListEl.innerHTML = '<p class="inspector-note">No top-level functions found.</p>';
          return;
        }
        const byName = {};
        funcs.forEach(f => { byName[f.name] = f; });

        funcs.forEach(f => {
          const det = document.createElement('details');
          det.className = 'callgraph-func';
          det.open = funcs.length <= 6;

          const sum = document.createElement('summary');
          const nameBtn = document.createElement('button');
          nameBtn.className = 'callgraph-name';
          nameBtn.textContent = f.name + (f.recursive ? ' ↺' : '');
          nameBtn.title = 'Jump to definition (line ' + f.line + ')';
          nameBtn.addEventListener('click', (ev) => {
            ev.preventDefault();
            ev.stopPropagation();
            jumpToSource(f.line, 1);
          });
          sum.appendChild(nameBtn);

          const counts = document.createElement('span');
          counts.className = 'callgraph-counts';
          const callCount = f.calls ? f.calls.length : 0;
          const callerCount = f.calledBy ? f.calledBy.length : 0;
          let countsText = callCount + ' call' + (callCount === 1 ? '' : 's') + ' · ' +
            callerCount + ' caller' + (callerCount === 1 ? '' : 's');
          if (f.complexity) countsText += ' · complexity ' + f.complexity;
          if (f.loc) countsText += ' · ' + f.loc + ' LOC';
          counts.textContent = countsText;
          sum.appendChild(counts);
          det.appendChild(sum);

          const body = document.createElement('div');
          body.className = 'callgraph-body';
          body.appendChild(buildCallGraphColumn('Calls', f.calls, (c) => ({
            label: c.name,
            title: 'Jump to call site (line ' + c.line + ')',
            resolved: !!c.resolved,
            onClick: () => jumpToSource(c.line, 1)
          })));
          body.appendChild(buildCallGraphColumn('Called by', f.calledBy, (callerName) => {
            const caller = byName[callerName];
            let jumpLine = caller ? caller.line : null;
            if (caller && caller.calls) {
              const site = caller.calls.find(c => c.resolved === f.name);
              if (site) jumpLine = site.line;
            }
            return {
              label: callerName,
              title: jumpLine ? 'Jump to call site' : 'Location unavailable',
              resolved: true,
              onClick: () => { if (jumpLine) jumpToSource(jumpLine, 1); }
            };
          }, '— (entry point or unresolved)'));

          det.appendChild(body);
          callGraphListEl.appendChild(det);
        });
      }

      function buildCallGraphColumn(title, entries, toItem, emptyLabel) {
        const col = document.createElement('div');
        col.className = 'callgraph-col';
        const titleEl = document.createElement('div');
        titleEl.className = 'callgraph-col-title';
        titleEl.textContent = title;
        col.appendChild(titleEl);
        if (entries && entries.length) {
          entries.forEach(entry => {
            const info = toItem(entry);
            const item = document.createElement('button');
            item.className = 'callgraph-edge' + (info.resolved ? ' callgraph-resolved' : '');
            item.textContent = info.label;
            item.title = info.title || '';
            item.addEventListener('click', info.onClick);
            col.appendChild(item);
          });
        } else {
          const none = document.createElement('span');
          none.className = 'callgraph-none';
          none.textContent = emptyLabel || '—';
          col.appendChild(none);
        }
        return col;
      }

      // Call graph diagram: the same AnalyzeCallGraph result as the list
      // view, laid out as boxes and arrows instead of text columns. Layout
      // is a simple longest-path layering (roots = functions with no local
      // caller, at column 0; each callee sits one column to the right of
      // its deepest-so-far caller) — good enough for the small, mostly-DAG
      // call graphs a playground snippet produces, without pulling in a
      // graph-layout library. Cycles (mutual or direct recursion) are
      // handled defensively (see the step cap below) rather than laid out
      // "correctly", since there is no single correct layering for a cycle.
      const SVG_NS = 'http://www.w3.org/2000/svg';
      const CG_NODE_H = 34;
      const CG_NODE_GAP_Y = 14;
      const CG_COL_GAP_X = 96;
      const CG_PAD = 32; // headroom for skip-edge curves bowing above/below the outermost node rows

      function svgEl(tag, attrs) {
        const el = document.createElementNS(SVG_NS, tag);
        if (attrs) for (const k in attrs) el.setAttribute(k, attrs[k]);
        return el;
      }

      function cgNodeWidth(label) {
        return Math.max(64, Math.min(220, label.length * 7.2 + 28));
      }

      function computeCallGraphLevels(funcs) {
        const byName = new Map(funcs.map(f => [f.name, f]));
        const level = new Map();
        const selfLoops = new Set();
        const queue = [];
        funcs.forEach(f => {
          if (!f.calledBy || f.calledBy.length === 0) { level.set(f.name, 0); queue.push(f.name); }
        });
        // Every function must end up with a level even if none qualified as
        // a root (e.g. a pure cycle with no unambiguous entry point).
        funcs.forEach(f => { if (!level.has(f.name)) { level.set(f.name, 0); queue.push(f.name); } });

        const maxSteps = Math.max(200, funcs.length * 20);
        let head = 0, steps = 0;
        while (head < queue.length && steps < maxSteps) {
          steps++;
          const name = queue[head++];
          const f = byName.get(name);
          if (!f || !f.calls) continue;
          const lvl = level.get(name);
          for (const c of f.calls) {
            if (!c.resolved || !byName.has(c.resolved)) continue;
            if (c.resolved === name) { selfLoops.add(name); continue; }
            const candidate = lvl + 1;
            const existing = level.get(c.resolved);
            if (existing === undefined || candidate > existing) {
              level.set(c.resolved, candidate);
              queue.push(c.resolved);
            }
          }
        }
        return { level, selfLoops, byName };
      }

      function renderCallGraphDiagram(result) {
        if (!callGraphDiagramEl) return;
        callGraphDiagramEl.innerHTML = '';
        const funcs = result.funcs || [];
        if (funcs.length === 0) {
          callGraphDiagramEl.innerHTML = '<p class="inspector-note">No top-level functions found.</p>';
          return;
        }

        const { level, selfLoops, byName } = computeCallGraphLevels(funcs);
        const maxInternalLevel = Math.max(0, ...Array.from(level.values()));

        // External calls (unresolved: fmt.Println, an interface method, ...)
        // are collapsed into one shared node per unique name, so ten
        // functions all calling fmt.Println draw one box with ten incoming
        // arrows rather than ten duplicate boxes. They form the rightmost
        // column since they have no further internal structure to show.
        const externalLevel = maxInternalLevel + 1;
        const externalNodes = new Map(); // name -> { name, callers: Set }
        funcs.forEach(f => {
          (f.calls || []).forEach(c => {
            if (c.resolved) return;
            let ext = externalNodes.get(c.name);
            if (!ext) { ext = { name: c.name, callers: new Set() }; externalNodes.set(c.name, ext); }
            ext.callers.add(f.name);
          });
        });

        // Group into columns.
        const columns = [];
        for (let i = 0; i <= externalLevel; i++) columns.push([]);
        funcs.forEach(f => columns[level.get(f.name)].push({ kind: 'func', name: f.name, data: f }));
        externalNodes.forEach(ext => columns[externalLevel].push({ kind: 'external', name: ext.name, data: ext }));
        if (columns[externalLevel].length === 0) columns.pop();

        // Position every node: x by column, y stacked within its column.
        const pos = new Map(); // name -> {x, y, w, h, kind}
        let colX = CG_PAD;
        const colWidths = columns.map(col => col.reduce((w, n) => Math.max(w, cgNodeWidth(n.name)), 64));
        columns.forEach((col, ci) => {
          let y = CG_PAD;
          col.forEach(n => {
            const w = colWidths[ci];
            pos.set(n.name, { x: colX, y, w, h: CG_NODE_H, kind: n.kind, data: n.data, col: ci });
            y += CG_NODE_H + CG_NODE_GAP_Y;
          });
          colX += colWidths[ci] + CG_COL_GAP_X;
        });
        const totalWidth = colX - CG_COL_GAP_X + CG_PAD;
        const totalHeight = Math.max(...columns.map(col => col.length), 1) * (CG_NODE_H + CG_NODE_GAP_Y) + CG_PAD * 2;

        const svg = svgEl('svg', {
          width: totalWidth, height: totalHeight,
          viewBox: '0 0 ' + totalWidth + ' ' + totalHeight,
          class: 'callgraph-svg'
        });
        const defs = svgEl('defs');
        defs.innerHTML =
          '<marker id="cg-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">' +
          '<path d="M0,0 L10,5 L0,10 z" fill="var(--accent)"></path></marker>' +
          '<marker id="cg-arrow-muted" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">' +
          '<path d="M0,0 L10,5 L0,10 z" fill="var(--text-dim)"></path></marker>';
        svg.appendChild(defs);

        const edgeLayer = svgEl('g', { class: 'callgraph-edges' });
        const nodeLayer = svgEl('g', { class: 'callgraph-nodes' });

        // Edges first, so node boxes paint over the line ends cleanly.
        // An edge whose caller and callee sit two or more columns apart
        // (e.g. main -> a helper's own external call) would otherwise pass
        // in a dead-straight line right through whatever node happens to
        // sit in an intervening column at the same row — indistinguishable
        // from actually touching it. skipBow arcs the curve's midpoint away
        // from that straight line, growing with how many columns it clears
        // and alternating up/down (via a running counter) so several such
        // edges fan out instead of stacking on the same arc.
        let skipEdgeCount = 0;
        funcs.forEach(f => {
          const from = pos.get(f.name);
          if (!from) return;
          (f.calls || []).forEach(c => {
            const targetName = c.resolved || null;
            const to = targetName ? pos.get(targetName) : pos.get(c.name);
            if (!to || (targetName === f.name)) return; // self-loop drawn separately
            const muted = !c.resolved;
            const x1 = from.x + from.w, y1 = from.y + from.h / 2;
            const x2 = to.x, y2 = to.y + to.h / 2;
            const midX = (x1 + x2) / 2;
            const colSpan = to.col - from.col;
            let midY1 = y1, midY2 = y2;
            if (colSpan > 1) {
              skipEdgeCount++;
              const dir = skipEdgeCount % 2 === 0 ? -1 : 1;
              const bow = dir * Math.min(18 + 9 * (colSpan - 1), CG_PAD - 4);
              midY1 += bow;
              midY2 += bow;
            }
            const path = svgEl('path', {
              d: 'M ' + x1 + ' ' + y1 + ' C ' + midX + ' ' + midY1 + ', ' + midX + ' ' + midY2 + ', ' + x2 + ' ' + y2,
              class: 'callgraph-edge-path' + (muted ? ' muted' : ''),
              'marker-end': muted ? 'url(#cg-arrow-muted)' : 'url(#cg-arrow)'
            });
            const title = svgEl('title');
            title.textContent = f.name + ' → ' + c.name + ' (line ' + c.line + ')';
            path.appendChild(title);
            edgeLayer.appendChild(path);
          });
        });

        // Self-recursion: a small loop bulging out to the right of the node.
        selfLoops.forEach(name => {
          const n = pos.get(name);
          if (!n) return;
          const x = n.x + n.w, yTop = n.y + n.h * 0.28, yBot = n.y + n.h * 0.72;
          const bulge = x + 26;
          const path = svgEl('path', {
            d: 'M ' + x + ' ' + yTop + ' C ' + bulge + ' ' + yTop + ', ' + bulge + ' ' + yBot + ', ' + x + ' ' + yBot,
            class: 'callgraph-edge-path callgraph-selfloop',
            'marker-end': 'url(#cg-arrow)'
          });
          const title = svgEl('title');
          title.textContent = name + ' calls itself (recursion)';
          path.appendChild(title);
          edgeLayer.appendChild(path);
        });

        // Nodes.
        pos.forEach((n, name) => {
          const g = svgEl('g', {
            class: 'callgraph-node' + (n.kind === 'external' ? ' callgraph-node-external' : '') +
              (n.kind === 'func' && (!n.data.calledBy || n.data.calledBy.length === 0) ? ' callgraph-node-entry' : '')
          });
          g.appendChild(svgEl('rect', { x: n.x, y: n.y, width: n.w, height: n.h, rx: 7 }));
          const text = svgEl('text', { x: n.x + n.w / 2, y: n.y + n.h / 2 + 1 });
          text.textContent = name;
          g.appendChild(text);
          if (n.kind === 'func') {
            const title = svgEl('title');
            const callCount = (n.data.calls || []).length, callerCount = (n.data.calledBy || []).length;
            title.textContent = name + ' — line ' + n.data.line + ' · ' + callCount + ' call(s) · ' + callerCount + ' caller(s)\nClick to jump to definition';
            g.appendChild(title);
            g.style.cursor = 'pointer';
            g.addEventListener('click', () => jumpToSource(n.data.line, 1));
          } else {
            const title = svgEl('title');
            title.textContent = name + ' — external/unresolved call, from: ' + Array.from(n.data.callers).join(', ');
            g.appendChild(title);
          }
          nodeLayer.appendChild(g);
        });

        svg.appendChild(edgeLayer);
        svg.appendChild(nodeLayer);

        const wrap = document.createElement('div');
        wrap.className = 'callgraph-svg-wrap';
        wrap.appendChild(svg);
        callGraphDiagramEl.appendChild(wrap);

        const legend = document.createElement('div');
        legend.className = 'callgraph-legend';
        legend.innerHTML =
          '<span><i class="cg-swatch cg-swatch-entry"></i>Entry point</span>' +
          '<span><i class="cg-swatch cg-swatch-func"></i>Function / method</span>' +
          '<span><i class="cg-swatch cg-swatch-external"></i>External / unresolved call</span>' +
          '<span class="inspector-note">Click a function box to jump to its definition.</span>';
        callGraphDiagramEl.appendChild(legend);
      }

      function requestCallGraph() {
        if (!worker) { logMessage('Worker not ready', 'warn'); return; }
        if (callGraphBtn) { callGraphBtn.disabled = true; callGraphBtn.textContent = 'Analyzing…'; }
        const source = getSource();
        callGraphRequestSources.push(source);
        worker.postMessage({ type: 'callgraph', source });
      }

      function renderBench(result) {
        if (!benchResultsEl) return;
        if (benchMetaEl) {
          benchMetaEl.textContent = formatSteps(result.stepsPerOp) + ' steps/op · avg ' + result.avgMs.toFixed(2) + 'ms';
        }
        const rows = (result.runsMs || []).map((ms, i) =>
          '<tr><td>#' + (i + 1) + '</td><td>' + ms.toFixed(2) + ' ms</td></tr>').join('');
        benchResultsEl.innerHTML =
          '<div class="bench-summary">' +
            '<div class="stat-chip"><span class="stat-value">' + formatSteps(result.stepsPerOp) + '</span><span class="stat-label">steps/op (deterministic)</span></div>' +
            '<div class="stat-chip"><span class="stat-value">' + result.avgMs.toFixed(2) + 'ms</span><span class="stat-label">avg</span></div>' +
            '<div class="stat-chip"><span class="stat-value">' + result.minMs.toFixed(2) + 'ms</span><span class="stat-label">min</span></div>' +
            '<div class="stat-chip"><span class="stat-value">' + result.maxMs.toFixed(2) + 'ms</span><span class="stat-label">max</span></div>' +
          '</div>' +
          '<table class="inspector-table"><thead><tr><th>Run</th><th>Wall time</th></tr></thead><tbody>' + rows + '</tbody></table>' +
          (result.suppressedMessages ? '<p class="inspector-note">' + result.suppressedMessages + ' output message(s) suppressed during benchmarking.</p>' : '');
      }

      function requestBench() {
        if (!worker) { logMessage('Worker not ready', 'warn'); return; }
        const iterations = Math.max(1, Math.min(100, parseInt(benchIterationsEl && benchIterationsEl.value, 10) || 5));
        const wantProfile = !!(heatmapToggle && heatmapToggle.checked);
        clearHeatmap();
        if (benchBtn) { benchBtn.disabled = true; benchBtn.textContent = 'Benchmarking…'; }
        if (benchSectionEl) benchSectionEl.open = true;
        setStatus('Benchmarking…', 'loading');
        showOutputPanel('inspector');
        logMessage('📊 Benchmarking ' + iterations + ' iteration(s)…', 'system');
        worker.postMessage({ type: 'bench', source: getSource(), iterations, profile: wantProfile });
      }

      const TRACE_RENDER_LIMIT = 500;

      function renderTrace(events, traceCap) {
        if (!traceTableEl) return;
        if (traceSectionEl) traceSectionEl.open = true;
        const total = events.length;
        if (traceMetaEl) {
          traceMetaEl.textContent = total + ' event(s)' +
            (traceCap && total >= traceCap ? ' (ring buffer full — oldest dropped)' : '');
        }
        if (total === 0) {
          traceTableEl.innerHTML = '<p class="inspector-note">No events recorded.</p>';
          return;
        }
        const shown = events.slice(0, TRACE_RENDER_LIMIT);
        const rows = shown.map(ev => {
          const loc = ev.line ? ev.line + (ev.col ? ':' + ev.col : '') : '';
          return '<tr class="trace-' + escapeHtml(ev.kind) + '">' +
            '<td>' + ev.seq + '</td>' +
            '<td>+' + (ev.ms || 0).toFixed(2) + '</td>' +
            '<td><span class="trace-kind">' + escapeHtml(ev.kind) + '</span></td>' +
            '<td>' + escapeHtml(ev.fn || '') + '</td>' +
            (loc ? '<td><button class="trace-loc" data-line="' + ev.line + '" data-col="' + (ev.col || 1) + '">' + loc + '</button></td>' : '<td></td>') +
            '<td>' + escapeHtml(ev.msg || '') + '</td>' +
          '</tr>';
        }).join('');
        traceTableEl.innerHTML =
          '<table class="inspector-table"><thead><tr><th>#</th><th>+ms</th><th>Kind</th><th>Function</th><th>Loc</th><th>Message</th></tr></thead><tbody>' +
          rows + '</tbody></table>' +
          (total > TRACE_RENDER_LIMIT ? '<p class="inspector-note">Showing first ' + TRACE_RENDER_LIMIT + ' of ' + total + ' events.</p>' : '');
      }

      if (traceTableEl) {
        traceTableEl.addEventListener('click', (ev) => {
          const btn = ev.target.closest('.trace-loc');
          if (btn) jumpToSource(parseInt(btn.dataset.line, 10), parseInt(btn.dataset.col, 10));
        });
      }

      if (astBtn) astBtn.onclick = requestAst;
      if (callGraphBtn) callGraphBtn.onclick = requestCallGraph;
      if (benchBtn) benchBtn.onclick = requestBench;
      if (inspectBtn) {
        inspectBtn.onclick = () => {
          showOutputPanel('inspector');
          requestAst();
          requestCallGraph();
        };
      }

      if (formatBtn) {
        formatBtn.onclick = () => {
          if (!worker) { logMessage('Worker not ready', 'warn'); return; }
          worker.postMessage({ type: 'format', source: getSource() });
        };
      }

      if (vetBtn) {
        vetBtn.onclick = () => {
          if (!worker) { logMessage('Worker not ready', 'warn'); return; }
          worker.postMessage({ type: 'vet', source: getSource() });
        };
      }

      if (testBtn) {
        testBtn.onclick = () => {
          if (!worker) { logMessage('Worker not ready', 'warn'); return; }
          if (isWorkspaceProject()) {
            requestWorkspaceTests();
            return;
          }
          worker.postMessage({ type: 'test', source: getSource(), filter: testFilterEl ? testFilterEl.value.trim() : '' });
        };
      }

      if (copyLogBtn) {
        copyLogBtn.onclick = () => {
          const text = Array.from(logEl.querySelectorAll('.log-line')).map(el => el.textContent).join('\n');
          navigator.clipboard.writeText(text).then(() => {
            copyLogBtn.textContent = 'Copied!';
            setTimeout(() => { copyLogBtn.textContent = 'Copy'; }, 1500);
          }, () => {
            copyLogBtn.textContent = 'Failed';
            setTimeout(() => { copyLogBtn.textContent = 'Copy'; }, 1500);
          });
        };
      }

      if (fileInput) {
        fileInput.addEventListener('change', (ev) => {
          const selected = Array.from(ev.target.files || []);
          if (!selected.length) return;
          Promise.all(selected.map(file => new Promise((resolve, reject) => {
            const reader = new FileReader();
            reader.onload = e => resolve({ name: file.name, source: e.target.result });
            reader.onerror = () => reject(reader.error || new Error('file read failed'));
            reader.readAsText(file);
          }))).then(entries => {
            let loaded = 0;
            entries.forEach(entry => {
              if (entry.name === 'go.mod') {
                saveActiveWorkspaceSource();
                workspaceFiles.set('go.mod', String(entry.source || ''));
                loaded++;
              } else if (addWorkspaceFile(entry.name, entry.source)) {
                loaded++;
              }
            });
            workspaceDirty = true;
            renderWorkspaceTabs();
            persistWorkspace();
            logMessage('📂 Loaded ' + loaded + ' workspace file(s)', 'system');
            showOutputPanel(loaded > 1 ? 'inspector' : 'console');
          }).catch(err => logMessage('❌ File load failed: ' + err.message, 'error'));
          // Reset input so the same file can be re-uploaded
          fileInput.value = '';
        });
      }

      if (fontSizeSelect) {
        const savedFontSize = localStorage.getItem('nanogo_font_size');
        if (savedFontSize) fontSizeSelect.value = savedFontSize;
        applyFontSize(fontSizeSelect.value);
        fontSizeSelect.addEventListener('change', () => {
          applyFontSize(fontSizeSelect.value);
          localStorage.setItem('nanogo_font_size', fontSizeSelect.value);
        });
      }

      function stopExecution() {
        if (!worker) { logMessage('No worker running', 'warn'); return; }
        try {
          worker.terminate();
        } catch (e) {}
        worker = null;
        if (execTimeEl) execTimeEl.textContent = '';
        setStatus('Stopped', 'error');
        logMessage('⛔ Execution terminated by user', 'system');
        // Reinitialize the worker so user can run again quickly
        setTimeout(() => startWasmWorker(), 300);
      }
      if (stopBtn) stopBtn.onclick = stopExecution;

      function fillExamples() {
        function tagsFor(config) {
          if (Array.isArray(config.tags) && config.tags.length) return config.tags;
          return [config.badge || 'Demo'];
        }

        // Re-render tag filters each time (collect fresh set of tags)
        function renderTagFilters() {
          if (!tagFilters) return;
          // Collect tags in insertion order. Legacy entries may still use
          // one `badge`; newer demos can belong to several categories.
          const tags = new Set();
          EXAMPLE_NAMES.forEach(k => { tagsFor(EXAMPLE_CONFIG[k]).forEach(tag => tags.add(tag)); });
          // Clear and add 'All'
          tagFilters.innerHTML = '';
          const allBtn = document.createElement('button');
          allBtn.className = 'tag-btn' + (selectedTag ? '' : ' active');
          allBtn.textContent = 'All';
          allBtn.onclick = () => { selectedTag = null; document.querySelectorAll('.tag-btn').forEach(b=>b.classList.remove('active')); allBtn.classList.add('active'); fillExamples(); };
          tagFilters.appendChild(allBtn);
          tags.forEach(t => {
            const b = document.createElement('button');
            b.className = 'tag-btn' + (selectedTag === t ? ' active' : '');
            b.textContent = t;
            b.onclick = () => { selectedTag = (selectedTag === t ? null : t); document.querySelectorAll('.tag-btn').forEach(btn=>btn.classList.remove('active')); if (selectedTag) b.classList.add('active'); else allBtn.classList.add('active'); fillExamples(); };
            tagFilters.appendChild(b);
          });
        }

        examplesList.innerHTML = "";
        renderTagFilters();
        const filter = exampleSearch && exampleSearch.value ? exampleSearch.value.trim().toLowerCase() : "";
        // With 50+ demos, a flat list makes browsing tedious. When nothing
        // is filtered or tag-selected, group by each example's primary
        // (first) tag instead — but any active search or tag filter is
        // already an implicit single grouping, so keep those flat.
        const showGroups = !filter && !selectedTag;

        function matchesFilters(name, config, exampleTags) {
          if (filter) {
            const hay = (name + ' ' + (config.description || '') + ' ' + exampleTags.join(' ')).toLowerCase();
            if (!hay.includes(filter)) return false;
          }
          if (selectedTag && !exampleTags.includes(selectedTag)) return false;
          return true;
        }

        function renderExampleItem(name, config, exampleTags) {
          const item = document.createElement('div');
          item.className = 'example-item';
          item.tabIndex = 0;
          item.dataset.example = name;
          item.onclick = () => selectExample(name);
          item.onkeypress = (e) => { if (e.key === 'Enter') selectExample(name); };

          const info = document.createElement('div');
          info.className = 'example-info';
          info.innerHTML = `<div class="example-name">${name}</div><div class="example-desc">${config.description}</div>`;

          const badges = document.createElement('div');
          badges.className = 'example-tags';
          exampleTags.forEach(tag => {
            const badge = document.createElement('div');
            badge.className = 'example-badge';
            badge.textContent = tag;
            badges.appendChild(badge);
          });

          const copyBtn = document.createElement('button');
          copyBtn.className = 'copy-btn';
          copyBtn.title = 'Copy example code';
          copyBtn.textContent = 'Copy';
          copyBtn.onclick = async (ev) => {
            ev.stopPropagation();
            copyBtn.disabled = true;
            copyBtn.textContent = '…';
            try {
              await ensureExamplesLoaded();
              await navigator.clipboard.writeText(getExampleSource(name) || '');
              copyBtn.textContent = 'Copied!';
              setTimeout(() => { copyBtn.textContent = 'Copy'; }, 1500);
            } catch (e) {
              copyBtn.textContent = 'Failed';
              setTimeout(() => { copyBtn.textContent = 'Copy'; }, 1500);
            } finally {
              copyBtn.disabled = false;
            }
          };

          const rightCol = document.createElement('div');
          rightCol.style.display = 'flex';
          rightCol.style.gap = '8px';
          rightCol.style.alignItems = 'center';
          rightCol.appendChild(badges);
          rightCol.appendChild(copyBtn);

          item.appendChild(info);
          item.appendChild(rightCol);

          examplesList.appendChild(item);
        }

        if (showGroups) {
          // Bucket by primary tag, preserving first-seen tag order and each
          // example's original order within its bucket.
          const buckets = new Map();
          EXAMPLE_NAMES.forEach(name => {
            if (name === 'Empty') return;
            const config = EXAMPLE_CONFIG[name] || { output: 'console', description: 'Go programming example', badge: 'Demo' };
            const exampleTags = tagsFor(config);
            const primary = exampleTags[0] || 'Demo';
            if (!buckets.has(primary)) buckets.set(primary, []);
            buckets.get(primary).push({ name, config, exampleTags });
          });
          buckets.forEach((items, tag) => {
            const header = document.createElement('div');
            header.className = 'example-group-header';
            header.textContent = tag;
            examplesList.appendChild(header);
            items.forEach(({ name, config, exampleTags }) => renderExampleItem(name, config, exampleTags));
          });
        } else {
          EXAMPLE_NAMES.forEach(name => {
            if (name === 'Empty') return; // Skip empty example
            const config = EXAMPLE_CONFIG[name] || { output: 'console', description: 'Go programming example', badge: 'Demo' };
            const exampleTags = tagsFor(config);
            if (!matchesFilters(name, config, exampleTags)) return;
            renderExampleItem(name, config, exampleTags);
          });
        }

        // Initialize enhanced logging (only once on first fill)
        if (!logLines || logLines.length === 0) initializeEnhancedLogging();
      }

      function initializeEnhancedLogging() {
        // Clear and setup initial log
        logEl.innerHTML = "";
        logLines = [];
        
        // Create initial welcome message
        const welcomeLines = [
          { text: "Welcome to nanoGo!", type: 'system' },
          { text: "Select an example from the sidebar to get started!", type: 'system' },
          { text: "", type: 'output' }
        ];

        welcomeLines.forEach(({ text, type }) => {
          const lineElement = document.createElement('div');
          lineElement.className = 'log-line log-' + type;
          lineElement.textContent = text;
          logEl.appendChild(lineElement);
          
          logLines.push({
            text: text,
            element: lineElement,
            timestamp: Date.now(),
            type: type
          });
        });

        // Ensure initial scroll to bottom
        setTimeout(() => {
          logEl.scrollTop = logEl.scrollHeight;
        }, 100);
      }

      async function selectExample(name) {
        try {
          await ensureExamplesLoaded();
        } catch (e) {
          logMessage('❌ Failed to load examples: ' + e.message, 'error');
          return;
        }
        if (!getExampleSource(name)) return;

        currentExample = name;
        resetWorkspace(getExampleSource(name), 'main.go');

        // Update active state
        document.querySelectorAll('.example-item').forEach(item => {
          item.classList.remove('active');
        });
        
        const activeItem = document.querySelector('.example-item[data-example="' + CSS.escape(name) + '"]');
        if (activeItem) activeItem.classList.add('active');

        // Show appropriate output panel
        const config = EXAMPLE_CONFIG[name] || { output: 'console' };
        showOutputPanel(config.output);

        logMessage(`📖 Loaded: ${name}`, 'system');
      }

      // Encode/decode the editor's current code as a URL-safe base64 blob
      // for the `#code=` hash — shared by the Share button, the Embed
      // popup, and the outgoing postMessage API below.
      function encodeCodeForURL(code) { return btoa(unescape(encodeURIComponent(code))); }
      function decodeCodeFromURL(b64) { return decodeURIComponent(atob(b64)); }
      function buildShareURL() { return location.origin + location.pathname + "#code=" + encodeCodeForURL(getSource()); }

      // Share functionality
      shareBtn.onclick = () => {
        navigator.clipboard.writeText(buildShareURL()).then(() => {
          setStatus("Share URL copied!", "ready");
          logMessage("📤 Share URL copied to clipboard", 'system');
          shareBtn.textContent = '✅ Copied!';
          setTimeout(() => shareBtn.textContent = '📤 Share', 2000);
        }, err => {
          logMessage("❌ Share copy failed: " + err, 'error');
        });
      };

      // ---------------- Embed popup: generate an <iframe> snippet ----------------
      function closeEmbedPopup() { if (embedPopup) embedPopup.setAttribute('aria-hidden', 'true'); }
      function buildEmbedSrc() {
        const params = new URLSearchParams();
        params.set('embed', '1');
        if (embedAutorunEl && embedAutorunEl.checked) params.set('autorun', '1');
        if (embedReadonlyEl && embedReadonlyEl.checked) params.set('readonly', '1');
        return location.origin + location.pathname + '?' + params.toString() + '#code=' + encodeCodeForURL(getSource());
      }
      function refreshEmbedSnippet() {
        if (!embedSnippetEl) return;
        const src = buildEmbedSrc();
        embedSnippetEl.value = `<iframe src="${src}"\n  width="640" height="420" style="border:1px solid #d9e2ef;border-radius:10px"\n  title="nanoGo playground" loading="lazy"></iframe>`;
      }
      if (embedBtn && embedPopup) {
        embedBtn.onclick = () => {
          if (themePopup) closeThemePopup();
          // embedBtn now lives inside #shareMenuPopup (see the Share menu
          // above), and #embedPopup renders at the same anchored corner of
          // .header-actions that #shareMenuPopup does -- leaving the Share
          // menu open here would show both stacked on top of each other.
          closeShareMenuPopup();
          const hidden = embedPopup.getAttribute('aria-hidden') !== 'false';
          if (hidden) { refreshEmbedSnippet(); embedPopup.setAttribute('aria-hidden', 'false'); }
          else closeEmbedPopup();
        };
        [embedAutorunEl, embedReadonlyEl].forEach(el => { if (el) el.addEventListener('change', refreshEmbedSnippet); });
        if (embedCopyBtn) embedCopyBtn.onclick = () => {
          navigator.clipboard.writeText(embedSnippetEl.value).then(() => {
            logMessage('🔗 Embed snippet copied to clipboard', 'system');
            embedCopyBtn.textContent = '✅ Copied!';
            setTimeout(() => embedCopyBtn.textContent = 'Copy snippet', 1600);
          }, err => logMessage('❌ Embed copy failed: ' + err, 'error'));
        };
        if (embedCopyLinkBtn) embedCopyLinkBtn.onclick = () => {
          navigator.clipboard.writeText(buildEmbedSrc()).then(() => {
            logMessage('🔗 Embed link copied to clipboard', 'system');
            embedCopyLinkBtn.textContent = '✅ Copied!';
            setTimeout(() => embedCopyLinkBtn.textContent = 'Copy link only', 1600);
          }, err => logMessage('❌ Embed copy failed: ' + err, 'error'));
        };
        document.addEventListener('click', (ev) => {
          if (embedPopup.getAttribute('aria-hidden') === 'false') {
            const path = ev.composedPath ? ev.composedPath() : (ev.path || []);
            if (!path.includes(embedPopup) && !path.includes(embedBtn)) closeEmbedPopup();
          }
        });
        document.addEventListener('keydown', (ev) => { if (ev.key === 'Escape') closeEmbedPopup(); });
      }

      // Download functionality
      downloadBtn.onclick = () => {
        const blob = new Blob([getSource()], { type: "text/plain" });
        const a = document.createElement('a');
        a.href = URL.createObjectURL(blob);
        a.download = "nanogo-pro-code.go";
        a.click();
        URL.revokeObjectURL(a.href);
        logMessage("💾 Code downloaded", 'system');
      };

      // Priority for the code shown on load: an explicit `#code=` share link,
      // then a `?example=Name` query param, then the persistent IDE workspace,
      // and finally the legacy single-file session.
      // Returns true once source code actually ended up in the editor, so
      // callers (the 'ready' handler) know whether it's safe to autorun.
      async function loadCodeFromURL() {
        const hash = location.hash.startsWith("#code=") ? location.hash.slice(6) : "";
        if (hash) {
          try {
            resetWorkspace(decodeCodeFromURL(hash), 'main.go');
            logMessage("📥 Code loaded from URL", 'system');
            showOutputPanel('console'); // Default to console for shared code
            return true;
          } catch (e) {
            logMessage("❌ Failed to load code from URL", 'error');
            return false;
          }
        }
        if (queryExample) {
          try {
            await ensureExamplesLoaded();
            if (getExampleSource(queryExample)) {
              await selectExample(queryExample);
              return true;
            }
            logMessage('⚠️ Unknown example in URL: ' + queryExample, 'warn');
          } catch (e) {
            logMessage('❌ Failed to load example from URL: ' + e.message, 'error');
          }
        }
        if (loadPersistedWorkspace()) {
          showOutputPanel(isWorkspaceProject() ? 'inspector' : 'console');
          return true;
        }
        const saved = localStorage.getItem("nanogo_last");
        if (saved && saved.trim().length > 0) {
          resetWorkspace(saved, 'main.go');
          logMessage("💾 Restored previous session", 'system');
          showOutputPanel('console');
        }
        return false;
      }

      // In embed mode, give the visitor a way out of the compact iframe view
      // — a link back to the full playground with the same code preloaded,
      // matching the "Edit on ..." affordance of similar embeddable widgets.
      function setupEmbedPopout() {
        if (!isEmbedMode || !embedPopoutLink) return;
        embedPopoutLink.href = location.origin + location.pathname + "#code=" + encodeCodeForURL(getSource());
        embedPopoutLink.hidden = false;
      }

      // ---------------- postMessage integration API ----------------
      // Lets a page hosting this playground in an <iframe> control it and
      // observe its output without reaching into the iframe's DOM. See the
      // README's "Embedding" section for the full, documented contract.
      //
      // Outgoing (this window -> window.parent):
      //   {type:'nanogo:ready'}                          — WASM loaded, ready to run
      //   {type:'nanogo:output', text, kind}             — one per console line (kind: output|warn|error|system)
      //   {type:'nanogo:done', elapsed, stats}            — a run finished
      //
      // Incoming (window.parent -> this window, via iframe.contentWindow.postMessage):
      //   {type:'nanogo:set-code', code}                 — replace the editor contents
      //   {type:'nanogo:set-workspace', files, modulePath} — replace the VFS workspace
      //   {type:'nanogo:run'}                             — trigger a run, as if Run was clicked
      //   {type:'nanogo:workspace-check'}                 — resolve project imports
      //   {type:'nanogo:workspace-run'}                   — run the complete workspace
      //   {type:'nanogo:workspace-test'}                  — run workspace tests
      //   {type:'nanogo:stop'}                            — stop the running program
      //
      // No origin allowlist is enforced on either side: like other
      // embeddable widgets (YouTube, CodeSandbox), this only ever affects
      // the widget's own editor/run state, not host page data, so accepting
      // messages from any ancestor frame is an intentional, low-risk
      // default. An integrator who needs to restrict this can still check
      // `event.origin` on their own side of the channel.
      function postToHost(msg) {
        if (window.parent && window.parent !== window) {
          try { window.parent.postMessage(msg, '*'); } catch (e) { /* ignore */ }
        }
      }
      window.addEventListener('message', (ev) => {
        const data = ev.data;
        if (!data || typeof data !== 'object') return;
        switch (data.type) {
          case 'nanogo:set-code':
            if (typeof data.code === 'string') resetWorkspace(data.code, 'main.go');
            break;
          case 'nanogo:set-workspace':
            if (Array.isArray(data.files)) {
              if (typeof data.modulePath === 'string' && data.modulePath.trim()) workspaceModulePath = data.modulePath.trim();
              replaceWorkspace(data.files, data.active || 'main.go');
            }
            break;
          case 'nanogo:run':
            runCode();
            break;
          case 'nanogo:workspace-check':
            requestWorkspaceCheck();
            break;
          case 'nanogo:workspace-run':
            runWorkspace();
            break;
          case 'nanogo:workspace-test':
            if (isWorkspaceProject()) requestWorkspaceTests();
            else if (testBtn) testBtn.click();
            break;
          case 'nanogo:stop':
            stopExecution();
            break;
        }
      });

      function registerServiceWorker() {
        if (!('serviceWorker' in navigator) || location.protocol === 'file:') return;
        window.addEventListener('load', () => {
          navigator.serviceWorker.register('./sw.js?1').catch((err) => {
            console.warn('service worker registration failed:', err);
          });
        }, { once: true });
      }

      // Keyboard shortcuts
      window.addEventListener('keydown', (ev) => {
        if ((ev.ctrlKey || ev.metaKey) && ev.key.toLowerCase() === 'k') {
          openCommandPalette();
          ev.preventDefault();
          return;
        }
        if (commandPaletteEl && !commandPaletteEl.hidden) {
          const items = commandPaletteListEl ? commandPaletteListEl.querySelectorAll('.command-item') : [];
          if (ev.key === 'ArrowDown' && items.length) {
            commandPaletteIndex = Math.min(commandPaletteIndex + 1, items.length - 1);
            renderCommandPalette();
            ev.preventDefault();
            return;
          }
          if (ev.key === 'ArrowUp' && items.length) {
            commandPaletteIndex = Math.max(commandPaletteIndex - 1, 0);
            renderCommandPalette();
            ev.preventDefault();
            return;
          }
          if (ev.key === 'Enter' && items[commandPaletteIndex]) {
            items[commandPaletteIndex].click();
            ev.preventDefault();
            return;
          }
          if (ev.key === 'Escape') {
            closeCommandPalette();
            ev.preventDefault();
            return;
          }
        }
        if ((ev.ctrlKey || ev.metaKey) && ev.key === 'Enter') {
          runCode();
          ev.preventDefault();
        }
      });

      // Initialize
      window.nanoGoPlayground = {
        getSource,
        setSource,
        analyzeCallGraph: requestCallGraph,
        jumpToSource,
        toggleBreakpoint,
        getBreakpoints: breakpointLineNumbers,
        getBreakpointHits: () => Array.from(breakpointHits, ([line, count]) => ({ line, count })),
        runDebug,
        debugStart: startLiveDebug,
        debugCommand,
        runWorkspace,
        checkWorkspace: requestWorkspaceCheck,
        getWorkspace: () => ({ modulePath: workspaceModulePath, active: activeFilePath, files: workspaceFileEntries() }),
        openCommandPalette,
        showPanel: showOutputPanel
      };
      setStatus('Loading WebAssembly...', 'loading');
      registerServiceWorker();
      startWasmWorker();
    })();
