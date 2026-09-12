const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const source = fs.readFileSync(path.join(__dirname, '../wasm_worker.js'), 'utf8');

function setup(extra = {}) {
  const sent = [];
  const self = { postMessage: msg => sent.push(msg) };
  const context = vm.createContext({ self, ...extra });
  vm.runInContext(source, context);
  return { self, sent, context, run: code => vm.runInContext(code, context) };
}

test('scalar output preserves batching and error ordering', () => {
  const { run, sent } = setup();
  run(`postConsoleFromGuest('log', 'Grüße 🌍'); postConsoleFromGuest('warn', '');
       postConsoleFromGuest('error', 'failure');`);
  assert.deepEqual(JSON.parse(JSON.stringify(sent)), [
    { type: 'batch', items: [{ type: 'log', text: 'Grüße 🌍' }, { type: 'warn', text: '' }] },
    { type: 'error', text: 'failure' }
  ]);
});

test('workspace JSON cache follows revisions and supports old WASM', () => {
  const { run, self } = setup();
  run(`cacheWorkspaceSnapshot({ workspaceRevision: 1, files: [{path:'main.go',source:'Grüße 🌍'}] });`);
  assert.equal(run('Array.isArray(workspaceInput(workspaceSnapshotFor({workspaceRevision:1})))'), true);
  self.nanoGoWorkspaceJSON = true;
  const first = run('workspaceInput(workspaceSnapshotFor({workspaceRevision:1}))');
  assert.equal(JSON.parse(first)[0].source, 'Grüße 🌍');
  run(`cacheWorkspaceSnapshot({workspaceRevision:2, files:[{path:'main.go',source:'new'}]});`);
  assert.equal(run('workspaceSnapshotFor({workspaceRevision:1})'), null);
  assert.equal(JSON.parse(run('workspaceInput(workspaceSnapshotFor({workspaceRevision:2}))'))[0].source, 'new');
});

for (const mime of ['application/wasm', 'application/octet-stream', null]) {
  test(`WASM startup fetches once with MIME ${mime}`, async () => {
    let fetches = 0, reads = 0, streaming = 0;
    const response = { ok: true, headers: { get: () => mime }, arrayBuffer: async () => { reads++; return new ArrayBuffer(0); } };
    const { run } = setup({ fetch: async () => { fetches++; return response; }, WebAssembly: {
      instantiateStreaming: async r => { assert.equal(r, response); streaming++; return {instance: 'ok'}; },
      instantiate: async () => ({instance: 'ok'})
    }});
    assert.equal(await run(`instantiateWasm('test.wasm', {})`), 'ok');
    assert.equal(fetches, 1);
    assert.equal(streaming, mime === 'application/wasm' ? 1 : 0);
    assert.equal(reads, mime === 'application/wasm' ? 0 : 1);
  });
}

test('WASM HTTP and compilation errors do not trigger duplicate downloads', async () => {
  for (const ok of [false, true]) {
    let fetches = 0;
    const { run } = setup({ fetch: async () => { fetches++; return {ok, status:404, headers:{get:()=> 'application/wasm'}}; },
      WebAssembly: {instantiateStreaming: async () => {throw new Error('compile failed');}} });
    await assert.rejects(run(`instantiateWasm('bad.wasm', {})`), ok ? /compile failed/ : /HTTP 404/);
    assert.equal(fetches, 1);
  }
});
