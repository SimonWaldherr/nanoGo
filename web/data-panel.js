// Structured host data for the playground. Console output has its own lifecycle.
(function () {
  'use strict';
  const get = id => document.getElementById(id);
  const inputs = get('runInputs');
  const status = get('resultStatus');
  const data = get('resultData');
  const diagnostic = get('runDiagnostic');
  const inputError = get('inputError');
  const copy = get('copyResultsBtn');
  let snapshot = null;
  let running = false;

  function readOptions() {
    inputError.textContent = '';
    try {
      const text = inputs.value || '{}';
      if (text.length > 1024 * 1024 || new TextEncoder().encode(text).length > 1024 * 1024) throw new Error('Inputs exceed the demo’s 1 MiB text limit.');
      const value = JSON.parse(text);
      if (!value || Array.isArray(value) || typeof value !== 'object') throw new Error('Inputs must be a JSON object with named values.');
      function validate(item, depth) {
        if (depth > 64) throw new Error('Inputs exceed 64 levels of nesting.');
        if (typeof item === 'number' && !Number.isFinite(item)) throw new Error('Inputs must contain finite numbers.');
        if (item && typeof item === 'object') Object.values(item).forEach(child => validate(child, depth + 1));
      }
      validate(value, 1);
      const options = {};
      // Empty settings keep URL integrations with older WASM builds working.
      if (Object.keys(value).length) options.inputs = value;
      const limit = get('runStepLimit').value;
      if (limit) {
        const maxSteps = Number(limit);
        if (!Number.isSafeInteger(maxSteps) || maxSteps <= 0) throw new Error('Step limit must be a positive integer.');
        options.limits = {maxSteps};
      }
      return options;
    } catch (error) {
      inputError.textContent = error.message;
      inputs.focus();
      throw error;
    }
  }
  function start() {
    running = true;
    snapshot = null;
    status.textContent = 'Running — results pending';
    data.textContent = '';
    diagnostic.textContent = '';
    copy.disabled = true;
    copy.textContent = 'Copy results';
  }
  function finish(message) {
    running = false;
    const stats = message.stats || {};
    const error = message.error || stats.error;
    const events = stats.results || [];
    const committed = stats.resultsCommitted === true && !error;
    const detail = message.diagnostic || stats.diagnostic;
    status.textContent = committed ? 'Completed · ' + events.length + ' result(s)' :
      events.length ? 'Partial · execution failed; results are uncommitted' :
      error ? 'Failed · no committed results' : 'Structured results unavailable in this runtime';
    snapshot = {committed, results:events};
    if (detail) snapshot.diagnostic = detail;
    else if (error) snapshot.error = error;
    data.textContent = JSON.stringify(events, null, 2);
    diagnostic.textContent = detail ? JSON.stringify(detail, null, 2) : error || '';
    copy.disabled = false;
  }
  function stop(reason) {
    if (!running) return;
    running = false;
    snapshot = null;
    status.textContent = reason;
    copy.disabled = true;
  }
  copy.onclick = async () => {
    if (!snapshot) return;
    try {
      await navigator.clipboard.writeText(JSON.stringify(snapshot, null, 2));
      copy.textContent = 'Copied';
    } catch (_) { copy.textContent = 'Copy failed — select the results below'; }
  };
  window.nanoGoDataPanel = {readOptions, start, finish, stop, get running() {return running;}};
})();
