const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../app.js'), 'utf8')
  .replace("await import('./nanogo.mjs')", '({ createNanoGo: mockCreateNanoGo })');

function setup(code) {
  const elements = new Map(), clients = [], copies = [];
  const element = () => ({
    value: '', textContent: '', disabled: false, appendChild() {},
    getContext: () => ({clearRect() {}})
  });
  const context = vm.createContext({
    TextDecoder, TextEncoder, Uint8Array, console,
    atob: input => Buffer.from(input, 'base64').toString('binary'),
    btoa: input => Buffer.from(input, 'binary').toString('base64'),
    location: {hash: '#code=' + Buffer.from(code).toString('base64'), origin:'http://localhost', pathname:'/minimal.html', protocol:'http:'},
    navigator: {clipboard: {writeText(text) { copies.push(text); return Promise.resolve(); }}},
    document: {
      getElementById(id) { if (!elements.has(id)) elements.set(id, element()); return elements.get(id); },
      createElement: element
    },
    window: {addEventListener() {}},
    async mockCreateNanoGo({onMessage}) {
      const client = {dispose() { this.disposed = true; }};
      clients.push(client);
      onMessage({type:'ready', capabilities:{}});
      return client;
    }
  });
  vm.runInContext(source, context);
  return {elements, clients, copies};
}

test('minimal frontend loads and shares UTF-8 source and literal percent signs', async () => {
  const code = 'package main\n// Grüße 🌍 100%\nfunc main() {}';
  const {elements, copies} = setup(code);
  assert.equal(elements.get('src').value, code);
  elements.get('shareBtn').onclick();
  await Promise.resolve();
  assert.equal(Buffer.from(copies[0].split('#code=')[1], 'base64').toString(), code);
});

test('Stop creates a fresh client and preserves editor changes', async () => {
  const {elements, clients} = setup('original');
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(elements.get('runBtn').disabled, false);
  elements.get('src').value = 'edited while running';
  elements.get('stopBtn').onclick();
  assert.equal(clients[0].disposed, true);
  assert.equal(clients.length, 2);
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(elements.get('src').value, 'edited while running');
  assert.equal(elements.get('runBtn').disabled, false);
});

test('reference frontend remains parseable as a classic script', () => {
  const original = fs.readFileSync(path.join(__dirname, '../app.js'), 'utf8');
  assert.doesNotThrow(() => new vm.Script(original, {filename: 'app.js'}));
  assert.match(original, /import\('\.\/nanogo\.mjs'\)/);
});
