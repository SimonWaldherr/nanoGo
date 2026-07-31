/* global mermaid */
// Go Execution Lab — a Mermaid projection of nanoGo's AST-backed call graph,
// plus a client-side "Execution Replay" overlay built from recorded trace
// events. This is intentionally a separate, dependency-light layer: the
// interpreter remains the source of truth, while this file owns
// visualization, source linking, breakpoint annotations, and now trace
// replay — all derived from data the interpreter sends. Breakpoint hits are
// recorded by the runtime and replayed here; the browser never blocks a run.
(function () {
  'use strict';

  const graphEl = document.getElementById('labGraph');
  const detailsEl = document.getElementById('labDetails');
  const statusEl = document.getElementById('labStatus');
  const analyzeBtn = document.getElementById('labAnalyzeBtn');
  const debugBtn = document.getElementById('labDebugBtn');
  const replayBarEl = document.getElementById('labReplayBar');
  const replayPrevBtn = document.getElementById('labReplayPrev');
  const replayPlayBtn = document.getElementById('labReplayPlay');
  const replayNextBtn = document.getElementById('labReplayNext');
  const replayInfoEl = document.getElementById('labReplayInfo');
  let currentGraph = null;
  let renderSerial = 0;
  let mermaidReady = null;

  // Trace replay state. traceEvents is the raw event array from the most
  // recent recorded run; traceValid is cleared on source-change since a
  // trace no longer corresponds to what's in the editor. callSequence /
  // nodeElByName / replayIndex only make sense once a graph has actually
  // been rendered with that trace applied (see applyHeat/render).
  let traceEvents = null;
  let traceValid = false;
  let callSequence = [];
  let nodeElByName = new Map();
  let replayIndex = -1;
  let replayTimer = null;

  function api() { return window.nanoGoPlayground; }

  function escapeLabel(value) {
    return String(value || '')
      .replace(/&/g, 'and')
      .replace(/["`]/g, "'")
      .replace(/[\[\]{}<>]/g, '')
      .replace(/\n/g, ' ');
  }

  function loadMermaid() {
    if (window.mermaid) return Promise.resolve(window.mermaid);
    if (mermaidReady) return mermaidReady;
    mermaidReady = new Promise((resolve, reject) => {
      const script = document.createElement('script');
      script.src = 'https://cdn.jsdelivr.net/npm/mermaid@11/dist/mermaid.min.js';
      script.async = true;
      script.crossOrigin = 'anonymous';
      script.onload = () => resolve(window.mermaid);
      script.onerror = () => reject(new Error('Mermaid could not be loaded. Check the network connection.'));
      document.head.appendChild(script);
    });
    return mermaidReady;
  }

  // buildHeat reconstructs an approximate call stack from a recorded
  // call_start/call_end event sequence to derive per-function and per-edge
  // hit counts, plus an ordered "which function started next" sequence for
  // the replay scrubber. This is inherently best-effort: trace events carry
  // no goroutine ID, so concurrent goroutines interleave onto one shared
  // stack here. Mismatches (a call_end that doesn't match the top of stack)
  // are handled by best-match-or-drop, never by throwing — a wrong or
  // missing attribution is an acceptable degradation, a crash is not.
  //
  // Method calls are traced under their bare method name (interp.Function's
  // Name field has no receiver-type prefix), while the static call graph
  // keys methods as "Type.Method" — bareToFull below resolves a bare trace
  // name back to the graph's key, the same "unique owner" heuristic
  // AnalyzeCallGraph itself uses for unresolved selector calls.
  function buildHeat(events, ids) {
    const bareToFull = new Map();
    for (const name of ids.keys()) {
      const bare = name.includes('.') ? name.slice(name.indexOf('.') + 1) : name;
      if (!bareToFull.has(bare)) bareToFull.set(bare, []);
      bareToFull.get(bare).push(name);
    }
    function resolve(evFn) {
      if (ids.has(evFn)) return evFn;
      const owners = bareToFull.get(evFn);
      return (owners && owners.length === 1) ? owners[0] : null;
    }

    const nodeHits = new Map();
    const edgeHits = new Map();
    const sequence = [];
    const stack = [];
    for (const ev of events) {
      if (ev.kind === 'breakpoint') {
        const name = resolve(ev.fn);
        sequence.push({ name, line: ev.line || 0, seq: ev.seq, ms: ev.ms || 0, kind: 'breakpoint' });
      } else if (ev.kind === 'call_start') {
        const name = resolve(ev.fn);
        if (name) {
          nodeHits.set(name, (nodeHits.get(name) || 0) + 1);
          const caller = stack.length ? stack[stack.length - 1] : null;
          if (caller) {
            const key = caller + '→' + name;
            edgeHits.set(key, (edgeHits.get(key) || 0) + 1);
          }
          sequence.push({ name, seq: ev.seq, ms: ev.ms || 0 });
        }
        stack.push(name || ev.fn);
      } else if (ev.kind === 'call_end') {
        let idx = -1;
        for (let i = stack.length - 1; i >= 0; i--) {
          if (stack[i] === ev.fn || stack[i] === resolve(ev.fn)) { idx = i; break; }
        }
        if (idx >= 0) stack.splice(idx, 1);
        else if (stack.length) stack.pop();
      }
    }
    return { nodeHits, edgeHits, sequence };
  }

  function heatTier(count, max) {
    if (!count || !max) return 0;
    if (count >= max * 0.66) return 3;
    if (count >= max * 0.33) return 2;
    return 1;
  }

  function modelFromGraph(result, heat) {
    const funcs = (result && result.funcs) || [];
    const ids = new Map();
    funcs.forEach((fn, index) => ids.set(fn.name, 'fn' + index));
    const lines = ['flowchart LR'];
    funcs.forEach((fn, index) => {
      const score = fn.complexity ? ' · c' + fn.complexity : '';
      const recursive = fn.recursive ? ' ↺' : '';
      lines.push('fn' + index + '["' + escapeLabel(fn.name) + recursive + ' · L' + fn.line + score + '"]');
    });
    const externals = new Map();
    const resolvedEdges = []; // { linkIndex, from: fnName, to: fnName }
    let linkIndex = 0;
    funcs.forEach((fn) => (fn.calls || []).forEach((call) => {
      const from = ids.get(fn.name);
      const to = call.resolved && ids.get(call.resolved);
      if (to) {
        lines.push(from + ' -->|' + escapeLabel('L' + call.line) + '| ' + to);
        resolvedEdges.push({ linkIndex, from: fn.name, to: call.resolved });
      } else {
        const key = call.name;
        if (!externals.has(key)) externals.set(key, 'ext' + externals.size);
        lines.push(from + ' -.-> ' + externals.get(key));
      }
      linkIndex++;
    }));
    externals.forEach((id, name) => lines.push(id + '(["' + escapeLabel(name) + '"])'));
    lines.push('classDef entry fill:#163b30,stroke:#4cda8f,color:#e8f7ea,stroke-width:2px;');
    lines.push('classDef external fill:#20242d,stroke:#7c8a9b,color:#b5c4e5,stroke-dasharray: 4 3;');
    lines.push('classDef heat1 fill:#2b3a2e,stroke:#4a8f5c,color:#dff5e6,stroke-width:1.5px;');
    lines.push('classDef heat2 fill:#5a4a1e,stroke:#d9ab2e,color:#fff3d0,stroke-width:2px;');
    lines.push('classDef heat3 fill:#5a2323,stroke:#f0645f,color:#ffe3e1,stroke-width:2.5px;');
    funcs.filter(fn => !(fn.calledBy || []).length).forEach(fn => lines.push('class ' + ids.get(fn.name) + ' entry;'));
    externals.forEach(id => lines.push('class ' + id + ' external;'));

    let sequence = [];
    if (heat) {
      const maxNode = Math.max(0, ...Array.from(heat.nodeHits.values()));
      const maxEdge = Math.max(0, ...Array.from(heat.edgeHits.values()));
      heat.nodeHits.forEach((count, name) => {
        const tier = heatTier(count, maxNode);
        if (tier > 0 && ids.has(name)) lines.push('class ' + ids.get(name) + ' heat' + tier + ';');
      });
      const edgeColors = { 1: '#4a8f5c', 2: '#d9ab2e', 3: '#f0645f' };
      const edgeWidths = { 1: 1.5, 2: 2.5, 3: 3.5 };
      resolvedEdges.forEach(({ linkIndex, from, to }) => {
        const count = heat.edgeHits.get(from + '→' + to) || 0;
        const tier = heatTier(count, maxEdge);
        if (tier > 0) lines.push('linkStyle ' + linkIndex + ' stroke:' + edgeColors[tier] + ',stroke-width:' + edgeWidths[tier] + 'px;');
      });
      sequence = heat.sequence;
    }

    return { definition: lines.join('\n'), ids, funcs, externalCount: externals.size, sequence };
  }

  function nodeIdFromElement(element, ids) {
    let node = element && element.closest && element.closest('.node');
    if (!node) return null;
    const candidates = [node.dataset.id, node.id].filter(Boolean);
    for (const candidate of candidates) {
      for (const [name, id] of ids) {
        if (candidate === id || candidate.includes('-' + id + '-') || candidate.includes(id)) return name;
      }
    }
    return null;
  }

  function showDetails(fn) {
    if (!detailsEl || !fn) return;
    const isBreakpoint = api().getBreakpoints().includes(fn.line);
    detailsEl.hidden = false;
    detailsEl.innerHTML = '';
    const title = document.createElement('strong');
    title.className = 'lab-detail-title';
    title.textContent = fn.name + (fn.recursive ? ' ↺' : '') + ' · line ' + fn.line;
    const meta = document.createElement('span');
    meta.className = 'lab-detail-meta';
    const calls = (fn.calls || []).length;
    const callers = (fn.calledBy || []).length;
    let metaText = calls + ' call' + (calls === 1 ? '' : 's') + ' · ' + callers + ' caller' + (callers === 1 ? '' : 's');
    if (fn.complexity) metaText += ' · complexity ' + fn.complexity;
    if (fn.loc) metaText += ' · ' + fn.loc + ' LOC';
    if (fn.recursive) metaText += ' · recursive';
    meta.textContent = metaText;
    const actions = document.createElement('div');
    actions.className = 'lab-detail-actions';
    const jump = document.createElement('button');
    jump.className = 'mini-btn';
    jump.textContent = 'Jump to code';
    jump.onclick = () => api().jumpToSource(fn.line, 1);
    const bp = document.createElement('button');
    bp.className = 'mini-btn' + (isBreakpoint ? ' active' : '');
    bp.textContent = isBreakpoint ? 'Remove breakpoint' : 'Set breakpoint';
    bp.onclick = () => { api().toggleBreakpoint(fn.line); showDetails(fn); };
    actions.append(jump, bp);
    detailsEl.append(title, meta, actions);
  }

  // --- Replay scrubber -----------------------------------------------
  function stopReplayTimer() {
    if (replayTimer) { clearInterval(replayTimer); replayTimer = null; }
    if (replayPlayBtn) replayPlayBtn.textContent = '▶';
  }

  function setActiveNode(name) {
    nodeElByName.forEach((el) => el.classList.remove('lab-node-active'));
    if (name && nodeElByName.has(name)) nodeElByName.get(name).classList.add('lab-node-active');
  }

  function updateReplayInfo() {
    if (!replayInfoEl) return;
    if (!callSequence.length) {
      replayInfoEl.textContent = 'No recorded run yet';
      return;
    }
    if (replayIndex < 0) {
      replayInfoEl.textContent = callSequence.length + ' event(s) recorded · press ▶ or ⏭ to step through';
      return;
    }
    const step = callSequence[replayIndex];
    const location = step.line ? ' · L' + step.line : '';
    const kind = step.kind === 'breakpoint' ? 'breakpoint' : 'call';
    replayInfoEl.textContent = kind + ' ' + (replayIndex + 1) + '/' + callSequence.length + ' · ' + (step.name || 'program') + location + ' · +' + step.ms.toFixed(2) + 'ms';
  }

  function goToReplayStep(index) {
    if (!callSequence.length) return;
    replayIndex = Math.max(0, Math.min(callSequence.length - 1, index));
    setActiveNode(callSequence[replayIndex].name);
    if (callSequence[replayIndex].kind === 'breakpoint' && callSequence[replayIndex].line) {
      api().jumpToSource(callSequence[replayIndex].line, 1);
    }
    updateReplayInfo();
    if (replayIndex >= callSequence.length - 1) stopReplayTimer();
  }

  function replayNext() {
    if (!callSequence.length) return;
    if (replayIndex >= callSequence.length - 1) { stopReplayTimer(); return; }
    goToReplayStep(replayIndex + 1);
  }

  function replayPrev() {
    stopReplayTimer();
    goToReplayStep(replayIndex - 1);
  }

  function replayTogglePlay() {
    if (!callSequence.length) return;
    if (replayTimer) { stopReplayTimer(); return; }
    if (replayIndex >= callSequence.length - 1) replayIndex = -1;
    replayPlayBtn.textContent = '⏸';
    replayTimer = setInterval(replayNext, 550);
  }

  if (replayPrevBtn) replayPrevBtn.addEventListener('click', replayPrev);
  if (replayNextBtn) replayNextBtn.addEventListener('click', replayNext);
  if (replayPlayBtn) replayPlayBtn.addEventListener('click', replayTogglePlay);

  async function render(result) {
    currentGraph = result;
    if (!graphEl) return;
    stopReplayTimer();
    const heat = (traceValid && traceEvents) ? buildHeat(traceEvents, new Map((result.funcs || []).map((fn, i) => [fn.name, 'fn' + i]))) : null;
    const model = modelFromGraph(result, heat);
    callSequence = model.sequence || [];
    replayIndex = -1;
    if (replayBarEl) replayBarEl.hidden = !callSequence.length;
    updateReplayInfo();
    if (!model.funcs.length) {
      graphEl.innerHTML = '<p class="inspector-note">No functions found in the current source.</p>';
      return;
    }
    const token = ++renderSerial;
    graphEl.innerHTML = '<p class="inspector-note">Rendering Mermaid graph…</p>';
    try {
      const lib = await loadMermaid();
      if (token !== renderSerial) return;
      lib.initialize({ startOnLoad: false, securityLevel: 'strict', theme: 'base', themeVariables: {
        background: 'transparent', primaryColor: '#183c58', primaryTextColor: '#e9efff', primaryBorderColor: '#7ea8ff',
        lineColor: '#7ea8ff', secondaryColor: '#163b30', tertiaryColor: '#121a30', fontFamily: 'ui-monospace, SFMono-Regular, Menlo, monospace'
      } });
      const rendered = await lib.render('nanogo-mermaid-' + token, model.definition);
      if (token !== renderSerial) return;
      graphEl.innerHTML = rendered.svg;
      if (typeof rendered.bindFunctions === 'function') rendered.bindFunctions(graphEl);
      nodeElByName = new Map();
      graphEl.querySelectorAll('.node').forEach((node) => {
        const name = nodeIdFromElement(node, model.ids);
        if (!name) return;
        const fn = model.funcs.find(entry => entry.name === name);
        if (!fn) return;
        nodeElByName.set(name, node);
        node.setAttribute('role', 'button');
        node.setAttribute('tabindex', '0');
        node.setAttribute('aria-label', fn.name + ', line ' + fn.line);
        node.addEventListener('click', () => { api().jumpToSource(fn.line, 1); showDetails(fn); });
        node.addEventListener('keydown', (event) => {
          if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); api().jumpToSource(fn.line, 1); showDetails(fn); }
        });
      });
      const heatNote = heat ? ' · colored by ' + callSequence.length + ' recorded event(s)' : '';
      statusEl.textContent = model.funcs.length + ' function(s) · ' + model.externalCount + ' external call(s) · click nodes to navigate' + heatNote;
      document.dispatchEvent(new CustomEvent('nanogo:lab-graph', { detail: { functions: model.funcs.map(fn => ({ name: fn.name, line: fn.line })) } }));
    } catch (error) {
      graphEl.innerHTML = '';
      const message = document.createElement('p');
      message.className = 'inspector-error';
      message.textContent = 'Mermaid graph unavailable: ' + error.message;
      graphEl.appendChild(message);
      statusEl.textContent = 'Graph unavailable';
    }
  }

  if (analyzeBtn) analyzeBtn.addEventListener('click', () => {
    if (!api()) return;
    statusEl.textContent = 'Analyzing code…';
    api().analyzeCallGraph();
  });
  if (debugBtn) debugBtn.addEventListener('click', () => {
    if (!api() || typeof api().runDebug !== 'function') return;
    debugBtn.disabled = true;
    debugBtn.textContent = '🐞 Debugging…';
    api().runDebug();
  });
  document.addEventListener('nanogo:callgraph', (event) => render(event.detail));
  document.addEventListener('nanogo:breakpoints-change', () => {
    if (currentGraph && !detailsEl.hidden) render(currentGraph);
  });
  document.addEventListener('nanogo:source-change', () => {
    traceValid = false;
    if (currentGraph) statusEl.textContent = 'Code changed · visualize again to refresh the graph';
  });
  document.addEventListener('nanogo:trace', (event) => {
    traceEvents = (event.detail && event.detail.events) || [];
    traceValid = true;
    if (debugBtn) { debugBtn.disabled = false; debugBtn.textContent = '🐞 Debug run'; }
    if (currentGraph) render(currentGraph);
  });
  document.addEventListener('nanogo:breakpoint-hits', (event) => {
    const hits = (event.detail && event.detail.hits) || [];
    if (!statusEl || !hits.length) return;
    const total = hits.reduce((sum, hit) => sum + hit.count, 0);
    statusEl.textContent = total + ' breakpoint hit(s) · replay jumps to source lines';
  });

  window.nanoGoExecutionLab = {
    getGraphSummary: () => {
      const funcs = (currentGraph && currentGraph.funcs) || [];
      return funcs.map(fn => fn.name + ' (line ' + fn.line + '): calls ' + (fn.calls || []).map(call => call.name).join(', ')).join('\n');
    }
  };
})();
