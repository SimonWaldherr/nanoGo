// live_debug.js
// A small live pause/step debugger UI for the Lab panel, built on top of
// interp.DebugController via cmd/wasm's nanoGoDebug* exports. Unlike the
// existing Lab "Debug run" (deterministic record/replay — the worker
// finishes the whole run before anything is shown, see lab.js), this
// actually pauses the guest program mid-statement: pressing Continue/Step*
// sends a small message that resumes the exact paused goroutine. index.html
// forwards the underlying worker messages as `nanogo:debug-pause` /
// `nanogo:debug-done` CustomEvents; this file only ever talks to the rest
// of the playground through window.nanoGoPlayground, same as lab.js does.
(function () {
  'use strict';

  const startBtn = document.getElementById('liveDebugStartBtn');
  const pauseBtn = document.getElementById('liveDebugPauseBtn');
  const continueBtn = document.getElementById('liveDebugContinueBtn');
  const stepIntoBtn = document.getElementById('liveDebugStepIntoBtn');
  const stepOverBtn = document.getElementById('liveDebugStepOverBtn');
  const stepOutBtn = document.getElementById('liveDebugStepOutBtn');
  const stopBtn = document.getElementById('liveDebugStopBtn');
  const statusEl = document.getElementById('liveDebugStatus');
  const infoEl = document.getElementById('liveDebugInfo');
  const stackEl = document.getElementById('liveDebugStack');
  const varsBodyEl = document.querySelector('#liveDebugVarsTable tbody');

  if (!startBtn) return; // this build of index.html has no Lab panel

  function api() { return window.nanoGoPlayground; }

  // currentToken identifies whichever pause is currently displayed; it is
  // cleared as soon as a resume command is sent so a stray double-click
  // can't resume the same pause twice.
  let currentToken = null;

  function setButtons(mode) {
    // mode is 'idle' (nothing running), 'running' (free-running, only Pause
    // and Stop make sense), or 'paused' (waiting on a resume command).
    startBtn.disabled = mode !== 'idle';
    pauseBtn.disabled = mode !== 'running';
    const resuming = mode === 'paused';
    [continueBtn, stepIntoBtn, stepOverBtn, stepOutBtn].forEach((b) => { if (b) b.disabled = !resuming; });
    stopBtn.disabled = mode === 'idle';
  }

  function escapeHtml(s) {
    return String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  }

  function renderPause(info) {
    currentToken = info.token;
    infoEl.hidden = false;
    const loc = info.location || {};
    const where = loc.line ? ' · line ' + loc.line : '';
    statusEl.textContent = (info.reason || 'paused') + ' in ' + (info.function || 'program') + where;
    if (stackEl) stackEl.textContent = info.stack || '(no active call)';
    if (varsBodyEl) {
      const rows = (info.vars || []).map((v) =>
        '<tr><td>' + escapeHtml(v.name) + '</td><td title="' + escapeHtml(v.type || '') + '">' + escapeHtml(v.value) + '</td></tr>'
      );
      varsBodyEl.innerHTML = rows.length ? rows.join('') : '<tr><td colspan="2"><em>no local variables</em></td></tr>';
    }
    if (loc.line && api() && typeof api().jumpToSource === 'function') {
      api().jumpToSource(loc.line, loc.column || 1);
    }
    setButtons('paused');
  }

  function reset(finalText) {
    currentToken = null;
    infoEl.hidden = true;
    statusEl.textContent = finalText || 'Not running';
    setButtons('idle');
  }

  function resumeWith(command) {
    if (currentToken == null) return;
    const token = currentToken;
    currentToken = null;
    infoEl.hidden = true;
    statusEl.textContent = 'Running…';
    setButtons('running');
    api().debugCommand(command, { token: token });
  }

  startBtn.addEventListener('click', () => {
    if (!api() || typeof api().debugStart !== 'function') return;
    statusEl.textContent = 'Starting…';
    setButtons('running');
    api().debugStart();
  });
  pauseBtn.addEventListener('click', () => { if (api()) api().debugCommand('pause'); });
  stopBtn.addEventListener('click', () => {
    if (api()) api().debugCommand('stop');
    reset('Stopped');
  });
  continueBtn.addEventListener('click', () => resumeWith('continue'));
  stepIntoBtn.addEventListener('click', () => resumeWith('step-into'));
  stepOverBtn.addEventListener('click', () => resumeWith('step-over'));
  stepOutBtn.addEventListener('click', () => resumeWith('step-out'));

  document.addEventListener('nanogo:debug-pause', (ev) => renderPause(ev.detail));
  document.addEventListener('nanogo:debug-done', (ev) => {
    const result = ev.detail || {};
    reset(result.error ? 'Error: ' + result.error : 'Finished');
  });

  reset();
})();
