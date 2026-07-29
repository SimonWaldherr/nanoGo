// nanoGo AI — an opt-in browser client for OpenAI-compatible chat endpoints.
// It deliberately has no server component: the user chooses the endpoint,
// credentials remain in localStorage, and code is sent only on Ask.
(function () {
  'use strict';

  const endpointEl = document.getElementById('aiEndpoint');
  const modelEl = document.getElementById('aiModel');
  const keyEl = document.getElementById('aiApiKey');
  const includeGraphEl = document.getElementById('aiIncludeGraph');
  const promptEl = document.getElementById('aiPrompt');
  const askBtn = document.getElementById('aiAskBtn');
  const messagesEl = document.getElementById('aiMessages');
  const storageKey = 'nanogo_ai_config_v1';

  function readConfig() {
    try { return JSON.parse(localStorage.getItem(storageKey) || '{}'); } catch (_) { return {}; }
  }
  function saveConfig() {
    try {
      localStorage.setItem(storageKey, JSON.stringify({ endpoint: endpointEl.value.trim(), model: modelEl.value.trim(), key: keyEl.value, includeGraph: includeGraphEl.checked }));
    } catch (_) { /* private browsing may deny storage */ }
  }
  function restoreConfig() {
    const config = readConfig();
    if (config.endpoint) endpointEl.value = config.endpoint;
    if (config.model) modelEl.value = config.model;
    if (config.key) keyEl.value = config.key;
    if (typeof config.includeGraph === 'boolean') includeGraphEl.checked = config.includeGraph;
  }
  function addMessage(role, text) {
    const message = document.createElement('article');
    message.className = 'ai-message ai-message-' + role;
    const label = document.createElement('div');
    label.className = 'ai-message-label';
    label.textContent = role === 'user' ? 'You' : role === 'error' ? 'Connection' : 'nanoGo AI';
    const body = document.createElement('pre');
    body.textContent = text;
    message.append(label, body);
    messagesEl.appendChild(message);
    messagesEl.scrollTop = messagesEl.scrollHeight;
    return message;
  }
  function codeBlock(text) {
    const match = /```(?:go)?\s*\n([\s\S]*?)```/i.exec(text || '');
    return match && match[1] ? match[1].trim() + '\n' : '';
  }
  function appendApplyButton(message, text) {
    const code = codeBlock(text);
    if (!code) return;
    const button = document.createElement('button');
    button.className = 'mini-btn ai-apply-btn';
    button.textContent = 'Apply Go block to editor';
    button.onclick = () => {
      if (!window.nanoGoPlayground) return;
      window.nanoGoPlayground.setSource(code);
      button.textContent = 'Applied';
      button.disabled = true;
    };
    message.appendChild(button);
  }
  function contextFor(prompt) {
    const source = window.nanoGoPlayground ? window.nanoGoPlayground.getSource() : '';
    let context = 'User request:\n' + prompt;
    if (includeGraphEl.checked) {
      const graph = window.nanoGoExecutionLab && window.nanoGoExecutionLab.getGraphSummary ? window.nanoGoExecutionLab.getGraphSummary() : '';
      context += '\n\nCurrent nanoGo source:\n```go\n' + source + '\n```';
      if (graph) context += '\n\nStatic execution graph summary:\n' + graph;
    }
    return context;
  }
  async function ask() {
    const prompt = promptEl.value.trim();
    const endpoint = endpointEl.value.trim().replace(/\/$/, '');
    const model = modelEl.value.trim();
    if (!prompt) { promptEl.focus(); return; }
    if (!/^https?:\/\//i.test(endpoint) || !model) { addMessage('error', 'Enter a valid HTTP(S) endpoint and model name first.'); return; }
    saveConfig();
    addMessage('user', prompt);
    promptEl.value = '';
    askBtn.disabled = true;
    askBtn.textContent = 'Thinking…';
    try {
      const headers = { 'Content-Type': 'application/json' };
      if (keyEl.value) headers.Authorization = 'Bearer ' + keyEl.value;
      const response = await fetch(endpoint + '/chat/completions', {
        method: 'POST', headers,
        body: JSON.stringify({ model, temperature: 0.2, messages: [
          { role: 'system', content: 'You are nanoGo AI. Help with the supported nanoGo Go subset. Be concise, explain assumptions, and return a complete Go replacement only inside a fenced go code block. Never claim to have run code.' },
          { role: 'user', content: contextFor(prompt) }
        ] })
      });
      const data = await response.json().catch(() => ({}));
      if (!response.ok) throw new Error(data.error && data.error.message ? data.error.message : 'HTTP ' + response.status);
      const text = data.choices && data.choices[0] && data.choices[0].message && data.choices[0].message.content;
      if (!text) throw new Error('The endpoint returned no chat completion.');
      const message = addMessage('assistant', text);
      appendApplyButton(message, text);
    } catch (error) {
      addMessage('error', error.message + ' — check endpoint, API key, and CORS policy.');
    } finally {
      askBtn.disabled = false;
      askBtn.textContent = 'Ask AI';
    }
  }

  restoreConfig();
  [endpointEl, modelEl, keyEl, includeGraphEl].forEach(el => el && el.addEventListener('change', saveConfig));
  if (askBtn) askBtn.addEventListener('click', ask);
  if (promptEl) promptEl.addEventListener('keydown', (event) => {
    if ((event.ctrlKey || event.metaKey) && event.key === 'Enter') { event.preventDefault(); ask(); }
  });
})();
