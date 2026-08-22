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
  // stopping suppresses the "Error: nanogo: execution killed" message that
  // a user-initiated Stop naturally produces (Stop kills the interpreter,
  // which is exactly what should happen) — the user asked for this, so the
  // final status should read "Stopped", not "Error: ...".
  let stopping = false;

  function setButtons(mode) {
    // mode is 'idle' (nothing running), 'running' (free-running, only Pause
    // and Stop make sense), or 'paused' (waiting on a resume command).
    startBtn.disabled = mode !== 'idle';
    pauseBtn.disabled = mode !== 'running';
    const resuming = mode === 'paused';
    [continueBtn, stepIntoBtn, stepOverBtn, stepOutBtn].forEach((b) => { if (b) b.disabled = !resuming; });
    stopBtn.disabled = mode === 'idle';
  }

  // renderVarRow builds one editable row: name, a text input pre-filled
  // with the current value (editing it and pressing Enter or "Set" sends a
  // set-variable command for the pause currently displayed), and a spot for
  // an inline error if that assignment is rejected (e.g. an undefined name,
  // which can't happen from this UI, or a value expression that fails to
  // evaluate, e.g. a syntax error or a type mismatch).
  function renderVarRow(v) {
    const tr = document.createElement('tr');
    tr.dataset.varName = v.name;
    const nameTd = document.createElement('td');
    nameTd.textContent = v.name;
    nameTd.title = v.type || '';
    const valueTd = document.createElement('td');
    const input = document.createElement('input');
    input.type = 'text';
    input.className = 'live-debug-var-input';
    input.value = v.value;
    input.title = v.type || '';
    input.spellcheck = false;
    const setBtn = document.createElement('button');
    setBtn.type = 'button';
    setBtn.className = 'mini-btn';
    setBtn.textContent = 'Set';
    const msg = document.createElement('span');
    msg.className = 'live-debug-var-msg';
    const submit = () => sendSetVariable(v.name, input.value, msg);
    setBtn.addEventListener('click', submit);
    input.addEventListener('keydown', (ev) => { if (ev.key === 'Enter') submit(); });
    valueTd.appendChild(input);
    valueTd.appendChild(setBtn);
    valueTd.appendChild(msg);
    tr.appendChild(nameTd);
    tr.appendChild(valueTd);
    return tr;
  }

  function sendSetVariable(name, value, msgEl) {
    if (currentToken == null || !api()) return;
    msgEl.textContent = '';
    msgEl.classList.remove('live-debug-var-error');
    api().debugCommand('set-variable', { token: currentToken, name: name, value: value });
  }

  function renderPause(info) {
    currentToken = info.token;
    infoEl.hidden = false;
    const loc = info.location || {};
    const where = loc.line ? ' · line ' + loc.line : '';
    statusEl.textContent = (info.reason || 'paused') + ' in ' + (info.function || 'program') + where;
    if (stackEl) stackEl.textContent = info.stack || '(no active call)';
    if (varsBodyEl) {
      varsBodyEl.innerHTML = '';
      const vars = info.vars || [];
      if (!vars.length) {
        const tr = document.createElement('tr');
        const td = document.createElement('td');
        td.colSpan = 2;
        const em = document.createElement('em');
        em.textContent = 'no local variables';
        td.appendChild(em);
        tr.appendChild(td);
        varsBodyEl.appendChild(tr);
      } else {
        vars.forEach((v) => varsBodyEl.appendChild(renderVarRow(v)));
      }
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
    stopping = true;
    if (api()) api().debugCommand('stop');
    reset('Stopped');
  });
  continueBtn.addEventListener('click', () => resumeWith('continue'));
  stepIntoBtn.addEventListener('click', () => resumeWith('step-into'));
  stepOverBtn.addEventListener('click', () => resumeWith('step-over'));
  stepOutBtn.addEventListener('click', () => resumeWith('step-out'));

  document.addEventListener('nanogo:debug-command-result', (ev) => {
    const m = ev.detail || {};
    if (m.command !== 'set-variable' || !varsBodyEl) return;
    const row = varsBodyEl.querySelector('tr[data-var-name="' + (m.name || '').replace(/"/g, '') + '"]');
    if (!row) return;
    const input = row.querySelector('.live-debug-var-input');
    const msg = row.querySelector('.live-debug-var-msg');
    if (m.ok) {
      if (input && m.value != null) input.value = m.value;
      if (msg) { msg.textContent = '✓'; msg.classList.remove('live-debug-var-error'); }
    } else if (msg) {
      msg.textContent = m.error || 'failed';
      msg.classList.add('live-debug-var-error');
    }
  });

  document.addEventListener('nanogo:debug-pause', (ev) => renderPause(ev.detail));
  document.addEventListener('nanogo:debug-done', (ev) => {
    const result = ev.detail || {};
    if (stopping) {
      stopping = false;
      reset('Stopped');
      return;
    }
    reset(result.error ? 'Error: ' + result.error : 'Finished');
  });

  reset();
})();
