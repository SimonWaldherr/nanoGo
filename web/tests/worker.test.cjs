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

function startup(runGo) {
  const scripts = [], downloads = [], timers = new Set();
  let starts = 0;
  const host = setup({
    setTimeout(fn, ms) {
      const timer = setTimeout(() => { timers.delete(timer); fn(); }, ms);
      timers.add(timer);
      return timer;
    },
    clearTimeout(timer) { timers.delete(timer); clearTimeout(timer); },
    importScripts: url => scripts.push(url),
    fetch: async url => {
      downloads.push(url);
      return { ok: true, headers: {get: () => 'application/wasm'} };
    },
    WebAssembly: { instantiateStreaming: async () => ({ instance: {} }) },
    Go: class {
      importObject = {};
      run() { starts++; return runGo(host.self); }
    }
  });
  return { ...host, scripts, downloads, timers, starts: () => starts };
}

test('concurrent init shares one configured download and Go runtime', async () => {
  const host = startup(self => {
    self.nanoGoRun = () => '{}';
    self.nanoGoSignalReady();
    return new Promise(() => {});
  });
  await Promise.all([
    host.run("initWasmWorker({wasmURL:'custom.wasm?v=1',wasmExecURL:'custom_exec.js'})"),
    host.run('initWasmWorker()')
  ]);
  assert.deepEqual(host.scripts, ['custom_exec.js']);
  assert.deepEqual(host.downloads, ['custom.wasm?v=1']);
  assert.equal(host.starts(), 1);
  assert.equal(host.run('goReady'), true);
  assert.equal(host.timers.size, 0);
});

test('legacy WASM without readiness hook initializes and clears timers', async () => {
  const host = startup(self => {
    self.nanoGoRun = () => '{}';
    return new Promise(() => {});
  });
  await host.run('initWasmWorker()');
  assert.equal(host.run('goReady'), true);
  assert.equal(host.timers.size, 0);
});

test('Go startup rejection is reported immediately and clears polling timers', async () => {
  const host = startup(() => Promise.reject(new Error('incompatible Go runtime')));
  await host.self.onmessage({data: {type: 'init'}});
  assert.equal(host.sent[0].type, 'error');
  assert.equal(host.sent[0].fatal, true);
  assert.match(host.sent[0].text, /incompatible Go runtime/);
  assert.equal(host.timers.size, 0);
});

test('Go exit after readiness produces a fatal response', async () => {
  let exit;
  const host = startup(self => {
    self.nanoGoRun = () => '{}';
    self.nanoGoSignalReady();
    return new Promise(resolve => { exit = resolve; });
  });
  await host.run('initWasmWorker()');
  exit();
  await new Promise(resolve => setImmediate(resolve));
  assert.equal(host.run('goReady'), false);
  assert.equal(host.sent.at(-1).fatal, true);
  assert.match(host.sent.at(-1).text, /Go runtime exited/);
  await assert.rejects(host.run('initWasmWorker()'), /Go runtime exited/);
});

test('Go exit immediately after registering exports leaves the worker failed', async () => {
  const host = startup(self => {
    self.nanoGoRun = () => '{}';
    self.nanoGoSignalReady();
    return Promise.resolve();
  });
  await host.self.onmessage({data: {type:'init'}});
  assert.equal(host.run('goReady'), false);
  assert.equal(host.sent.at(-1).type, 'error');
  assert.equal(host.sent.at(-1).fatal, true);
  await assert.rejects(host.run('initWasmWorker()'), /Go runtime exited/);
  assert.equal(host.timers.size, 0);
});

for (const mode of ['stream', 'deferred']) {
  test(`${mode} thrown run flushes output before one terminal error response`, async () => {
    const host = setup();
    host.run(`goReady = true;
      self.nanoGoRun = () => {
        postConsoleFromGuest('log', 'before failure');
        throw new Error('run failed');
      };`);
    await host.self.onmessage({data: {type: 'run', source: '', mode}});
    assert.deepEqual(host.sent.map(message => message.type), ['log', 'error', 'done']);
    assert.match(host.sent[2].error, /run failed/);
    host.sent.length = 0;
    host.self.nanoGoRun = () => '{}';
    await host.self.onmessage({data: {type: 'run', source: '', mode}});
    assert.deepEqual(host.sent.map(message => message.type), ['done']);
  });
}

for (const supplied of ['bytes', 'module']) {
  test(`supplied ${supplied} and runtime start without fetch or importScripts`, async () => {
    class Module {}
    let instantiated;
    const host = setup({setTimeout, clearTimeout,
      fetch() {throw new Error('unexpected fetch');},
      importScripts() {throw new Error('unexpected import');},
      WebAssembly: {Module, instantiate:async asset => {
        instantiated = asset;
        return supplied === 'module' ? {} : {instance:{}};
      }},
      Go: class { importObject = {}; run() {
        host.self.nanoGoRun = () => '{}'; host.self.nanoGoSignalReady();
        return new Promise(() => {});
      }}
    });
    const asset = supplied === 'bytes' ? 'wasmBytes:new Uint8Array([0,97,115,109])' : 'wasmModule:new WebAssembly.Module()';
    await host.run(`initWasmWorker({protocolVersion:2,offline:true,wasmExecProvided:true,${asset}})`);
    assert.ok(instantiated);
    assert.equal(host.run('goReady'), true);
  });
}

test('supplied startup rejects missing runtime and malformed assets without network', async () => {
  for (const options of [
    '{offline:true}',
    '{offline:true,wasmBytes:new Uint8Array([0])}',
    '{offline:true,wasmBytes:new Uint8Array([0]),wasmExecProvided:true}',
    '{wasmModule:{},wasmExecProvided:true}',
    '{protocolVersion:999}'
  ]) {
    const host = setup({fetch() {throw new Error('unexpected network');}, importScripts() {throw new Error('unexpected network');}});
    await assert.rejects(host.run(`initWasmWorker(${options})`), error => !error.message.includes('unexpected network'));
  }
});

test('WASM compilation failures in supplied mode are fatal startup failures', async () => {
  const host = setup({
    Go: class { importObject = {}; },
    WebAssembly: {instantiate:async () => {throw new Error('invalid magic bytes');}}
  });
  await host.run(`self.onmessage({data:{type:'init',offline:true,wasmExecProvided:true,wasmBytes:new Uint8Array([0])}})`);
  assert.equal(host.sent[0].fatal, true);
  assert.match(host.sent[0].text, /invalid magic bytes/);
});

test('execution inputs and limits reach direct and workspace exports as JSON', async () => {
  const host = setup();
  host.run(`goReady = true;
    self.nanoGoVersion = () => JSON.stringify({hasStructuredResults:true});
    self.nanoGoRun = (...args) => { self.args = args; return '{}'; };
    self.nanoGoRunWorkspace = self.nanoGoRun;`);
  for (const type of ['run','workspace-run']) {
    await host.run(`self.onmessage({data:{type:'${type}',source:'test',files:[],inputs:{name:'Grüße',items:[1,null]},limits:{maxSteps:10}}})`);
    assert.deepEqual(JSON.parse(host.self.args[4]), {inputs:{name:'Grüße',items:[1,null]},limits:{maxSteps:10}});
  }
});

test('execution options reject unsupported JSON and cycles without running guest code', async () => {
  const host = setup();
  host.run(`goReady = true; self.nanoGoVersion = () => JSON.stringify({hasStructuredResults:true});
    self.nanoGoRun = () => { throw new Error('guest should not run'); };`);
  for (const value of ['NaN','undefined','() => 1','new Date()','(() => {const a={}; a.self=a; return a;})()',
    '(() => {let a={}; for(let i=0;i<65;i++) a={a}; return a;})()']) {
    host.sent.length = 0;
    await host.run(`self.onmessage({data:{type:'run',inputs:{value:${value}}}})`);
    const done = host.sent.at(-1);
    assert.equal(done.type, 'done');
    assert.match(done.error, /Execution options/);
    assert.doesNotMatch(done.error, /guest should not run/);
  }
});

test('legacy WASM rejects supplied inputs instead of ignoring them', async () => {
  const host = setup();
  host.run(`goReady=true; self.nanoGoRun=() => {throw new Error('should not run');};`);
  await host.self.onmessage({data:{type:'run',inputs:{}}});
  assert.match(host.sent.at(-1).error, /does not support inputs/);
});

test('tool failures preserve WASM diagnostic objects across worker replies', async () => {
  const host = setup();
  host.run(`goReady=true;
    self.failure = {error:'parse failed',diagnostic:{code:'parse.syntax',phase:'parse',message:'parse failed',location:{file:'input.go',line:2,column:3}}};
    self.nanoGoFormat = self.nanoGoVet = () => self.failure;
    self.nanoGoTest = self.nanoGoAst = self.nanoGoCallGraph = () => JSON.stringify(self.failure);`);
  for(const type of ['format','vet','test','ast','callgraph']) {
    host.sent.length=0;
    await host.self.onmessage({data:{type,source:'bad'}});
    const response=host.sent.at(-1);
    assert.equal((response.diagnostic || response.result?.diagnostic)?.code,'parse.syntax',type);
  }
});
