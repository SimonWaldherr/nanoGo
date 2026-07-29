/* global mermaid */
// Go Execution Lab — a Mermaid projection of nanoGo's AST-backed call graph.
// This is intentionally a separate, dependency-light layer: the interpreter
// remains the source of truth, while this file owns visualization, source
// linking, and breakpoint annotations.
(function () {
  'use strict';

  const graphEl = document.getElementById('labGraph');
  const detailsEl = document.getElementById('labDetails');
  const statusEl = document.getElementById('labStatus');
  const analyzeBtn = document.getElementById('labAnalyzeBtn');
  let currentGraph = null;
  let renderSerial = 0;
  let mermaidReady = null;

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

  function modelFromGraph(result) {
    const funcs = (result && result.funcs) || [];
    const ids = new Map();
    funcs.forEach((fn, index) => ids.set(fn.name, 'fn' + index));
    const lines = ['flowchart LR'];
    funcs.forEach((fn, index) => {
      const score = fn.metrics && fn.metrics.cyclomaticComplexity ? ' · c' + fn.metrics.cyclomaticComplexity : '';
      lines.push('fn' + index + '["' + escapeLabel(fn.name) + ' · L' + fn.line + score + '"]');
    });
    const externals = new Map();
    funcs.forEach((fn) => (fn.calls || []).forEach((call) => {
      const from = ids.get(fn.name);
      const to = call.resolved && ids.get(call.resolved);
      if (to) {
        lines.push(from + ' -->|' + escapeLabel('L' + call.line) + '| ' + to);
      } else {
        const key = call.name;
        if (!externals.has(key)) externals.set(key, 'ext' + externals.size);
        lines.push(from + ' -.-> ' + externals.get(key));
      }
    }));
    externals.forEach((id, name) => lines.push(id + '(["' + escapeLabel(name) + '"])'));
    lines.push('classDef entry fill:#163b30,stroke:#4cda8f,color:#e8f7ea,stroke-width:2px;');
    lines.push('classDef external fill:#20242d,stroke:#7c8a9b,color:#b5c4e5,stroke-dasharray: 4 3;');
    funcs.filter(fn => !(fn.calledBy || []).length).forEach(fn => lines.push('class ' + ids.get(fn.name) + ' entry;'));
    externals.forEach(id => lines.push('class ' + id + ' external;'));
    return { definition: lines.join('\n'), ids, funcs, externalCount: externals.size };
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
    title.textContent = fn.name + ' · line ' + fn.line;
    const meta = document.createElement('span');
    meta.className = 'lab-detail-meta';
    const calls = (fn.calls || []).length;
    const callers = (fn.calledBy || []).length;
    meta.textContent = calls + ' call' + (calls === 1 ? '' : 's') + ' · ' + callers + ' caller' + (callers === 1 ? '' : 's');
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

  async function render(result) {
    currentGraph = result;
    if (!graphEl) return;
    const model = modelFromGraph(result);
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
      graphEl.querySelectorAll('.node').forEach((node) => {
        const name = nodeIdFromElement(node, model.ids);
        if (!name) return;
        const fn = model.funcs.find(entry => entry.name === name);
        if (!fn) return;
        node.setAttribute('role', 'button');
        node.setAttribute('tabindex', '0');
        node.setAttribute('aria-label', fn.name + ', line ' + fn.line);
        node.addEventListener('click', () => { api().jumpToSource(fn.line, 1); showDetails(fn); });
        node.addEventListener('keydown', (event) => {
          if (event.key === 'Enter' || event.key === ' ') { event.preventDefault(); api().jumpToSource(fn.line, 1); showDetails(fn); }
        });
      });
      statusEl.textContent = model.funcs.length + ' function(s) · ' + model.externalCount + ' external call(s) · click nodes to navigate';
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
  document.addEventListener('nanogo:callgraph', (event) => render(event.detail));
  document.addEventListener('nanogo:breakpoints-change', () => {
    if (currentGraph && !detailsEl.hidden) render(currentGraph);
  });
  document.addEventListener('nanogo:source-change', () => {
    if (currentGraph) statusEl.textContent = 'Code changed · visualize again to refresh the graph';
  });

  window.nanoGoExecutionLab = {
    getGraphSummary: () => {
      const funcs = (currentGraph && currentGraph.funcs) || [];
      return funcs.map(fn => fn.name + ' (line ' + fn.line + '): calls ' + (fn.calls || []).map(call => call.name).join(', ')).join('\n');
    }
  };
})();
