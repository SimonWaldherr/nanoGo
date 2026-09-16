const { test } = require('node:test');
const assert = require('node:assert/strict');

async function setup(t, options = {}) {
  const { createNanoGo } = await import('../nanogo.mjs');
  let worker;
  class FakeWorker {
    constructor(url) { this.url = String(url); this.sent = []; worker = this; }
    postMessage(message) { this.sent.push(message); }
    terminate() { this.terminated = true; }
    emit(message) { this.onmessage({data: message}); }
  }
  t.mock.method(globalThis, 'Worker', function (url) { return new FakeWorker(url); });
  const pending = createNanoGo(options);
  // Avoid a global Worker requirement in Node by installing the property
  // once below. Each test restores its mock when it finishes.
  t.after(async () => { try { (await pending).dispose(); } catch {} });
  return { worker, pending, createNanoGo };
}

// Node has worker_threads instead of the browser Worker global.
if (!globalThis.Worker) globalThis.Worker = function Worker() {};

test('optional request deadline stops legacy workers without completion', async t => {
  const {worker, pending} = await setup(t, {requestTimeoutMs: 5});
  worker.emit({type:'ready'});
  const client = await pending;
  const run = assert.rejects(client.run('broken'), /request timed out/);
  const queued = assert.rejects(client.format('next'), /request timed out/);
  // Old workers sometimes reported only this message, without done.
  worker.emit({type:'error',text:'legacy failure'});
  await Promise.all([run, queued]);
  assert.equal(worker.terminated, true);
  assert.equal(worker.sent.length, 2); // init + run; queued format never sent
  worker.emit({type:'done'}); // a late completion cannot revive the queue
  await assert.rejects(client.run('later'), /request timed out/);
});

test('request deadline is cancelled on completion', async t => {
  const {worker, pending} = await setup(t, {requestTimeoutMs: 5});
  worker.emit({type:'ready'});
  const client = await pending;
  const run = client.run('ok');
  worker.emit({type:'done',stats:null});
  await run;
  await new Promise(resolve => setTimeout(resolve, 15));
  assert.equal(worker.terminated, undefined);
});

test('invalid request deadlines reject without constructing a worker', async t => {
  const {createNanoGo} = await import('../nanogo.mjs');
  t.mock.method(globalThis, 'Worker', () => { throw new Error('unexpected worker'); });
  for (const requestTimeoutMs of [-1, NaN, Infinity]) {
    await assert.rejects(createNanoGo({requestTimeoutMs}), /nonnegative finite/);
  }
});

test('client resolves module-relative default worker and waits for ready', async t => {
  const {worker, pending} = await setup(t);
  assert.match(worker.url, /\/web\/wasm_worker\.js$/);
  let ready = false;
  pending.then(() => { ready = true; });
  await Promise.resolve();
  assert.equal(ready, false);
  worker.emit({type: 'ready', capabilities: {hasWorkspace: true}});
  const client = await pending;
  assert.equal(client.capabilities.hasWorkspace, true);
});

test('client forwards asset overrides and serializes same-type requests', async t => {
  const {worker, pending} = await setup(t, {workerURL: '/sdk/worker.js', wasmURL:'release.wasm', wasmExecURL:'go.js'});
  assert.equal(worker.url, '/sdk/worker.js');
  assert.deepEqual(worker.sent[0], {type:'init', protocolVersion:2, wasmURL:'release.wasm', wasmExecURL:'go.js'});
  worker.emit({type:'ready'});
  const client = await pending;
  const first = client.format('first'), second = client.format('second');
  assert.equal(worker.sent.length, 2);
  worker.emit({type:'format-result', source:'first formatted'});
  assert.equal(worker.sent[2].source, 'second');
  worker.emit({type:'format-result', source:'second formatted'});
  assert.equal((await first).source, 'first formatted');
  assert.equal((await second).source, 'second formatted');
});

test('batch callback errors do not lose output or block request completion', async t => {
  const received = [], reported = [];
  t.mock.method(console, 'error', error => reported.push(error));
  const {worker, pending} = await setup(t, {onMessage(message) {
    received.push(message.type);
    if (message.type === 'log') throw new Error('render failed');
  }});
  worker.emit({type:'ready'});
  const client = await pending;
  const result = client.run('hello');
  worker.emit({type:'batch', items:[{type:'log',text:'one'}, {type:'warn',text:'two'}, {type:'done', stats:{steps:2}}]});
  assert.equal((await result).stats.steps, 2);
  assert.deepEqual(received, ['ready', 'log', 'warn', 'done']);
  assert.equal(reported.length, 1);
});

test('runtime errors reject after done and preserve the next request', async t => {
  const {worker, pending} = await setup(t);
  worker.emit({type:'ready'});
  const client = await pending;
  const failure = client.run('bad');
  const rejected = assert.rejects(failure, error => error.message === 'parse failed' && error.response.stats.steps === 0);
  const next = client.vet('good');
  worker.emit({type:'error',text:'parse failed'});
  assert.equal(worker.sent.length, 2);
  worker.emit({type:'done',stats:{error:'parse failed',steps:0}});
  assert.equal(worker.sent[2].type, 'vet');
  worker.emit({type:'vet-result',issues:[]});
  await rejected;
  assert.deepEqual((await next).issues, []);
});

test('workspace methods and tests return full structured responses', async t => {
  const {worker, pending} = await setup(t);
  worker.emit({type:'ready'});
  const client = await pending;
  const files = [{path:'main.go',source:'package main'}];
  for (const [method, requestType, responseType] of [
    ['checkWorkspace','workspace-check','workspace-check-result'],
    ['runWorkspace','workspace-run','workspace-done'],
    ['testWorkspace','workspace-test','workspace-test-result']
  ]) {
    const request = client[method](files, {modulePath:'demo'});
    assert.deepEqual(worker.sent.at(-1), {type:requestType,files,modulePath:'demo'});
    const response = {type:responseType,result:{passed:false,failed:1}};
    worker.emit(response);
    assert.equal(await request, response);
  }
  const tests = client.test('test source', {filter:'TestExample'});
  assert.equal(worker.sent.at(-1).filter, 'TestExample');
  worker.emit({type:'test-result',result:{passed:false,failed:1}});
  assert.equal((await tests).result.failed, 1);
});

for (const failure of ['dispose', 'error', 'messageerror', 'fatal']) {
  test(`${failure} rejects active, queued, and future operations`, async t => {
    const {worker, pending} = await setup(t);
    worker.emit({type:'ready'});
    const client = await pending;
    const outcomes = Promise.allSettled([client.run('one'), client.run('two')]);
    if (failure === 'dispose') client.dispose();
    else if (failure === 'error') worker.onerror({message:'worker crashed'});
    else if (failure === 'messageerror') worker.onmessageerror();
    else worker.emit({type:'error',fatal:true,text:'runtime exited'});
    assert.deepEqual((await outcomes).map(result => result.status), ['rejected','rejected']);
    assert.equal(worker.terminated, true);
    await assert.rejects(client.format('later'));
  });
}

test('init failure and timeout terminate workers and reject creation', async t => {
  const {worker, pending} = await setup(t);
  const rejected = assert.rejects(pending, /HTTP 404/);
  worker.emit({type:'error',text:'WASM init failed: HTTP 404'});
  await rejected;
  assert.equal(worker.terminated, true);
  const timed = await setup(t, {initTimeoutMs: 5});
  await assert.rejects(timed.pending, /timed out/);
  assert.equal(timed.worker.terminated, true);
});

test('invalid initialization timeout rejects before starting a worker', async t => {
  const { createNanoGo } = await import('../nanogo.mjs');
  for (const initTimeoutMs of [0, -1, Infinity, NaN]) {
    await assert.rejects(createNanoGo({initTimeoutMs}), /positive finite/);
  }
});

test('supplied assets snapshot byte views and negotiate protocol without URL fallbacks', async t => {
  const {createNanoGo, PROTOCOL_VERSION} = await import('../nanogo.mjs');
  const sent = [];
  const worker = { postMessage: message => sent.push(message), terminate() {} };
  const source = new Uint8Array([9, 0, 97, 115, 109, 9]);
  const pending = createNanoGo({offline: true, wasmBytes: source.subarray(1, 5),
    workerFactory: () => ({worker, wasmExecProvided: true})});
  source[1] = 99;
  assert.deepEqual(Array.from(new Uint8Array(sent[0].wasmBytes)), [0, 97, 115, 109]);
  assert.equal(sent[0].offline, true);
  assert.equal(sent[0].wasmExecProvided, true);
  assert.equal(sent[0].protocolVersion, PROTOCOL_VERSION);
  worker.onmessage({data: {type:'ready', protocolVersion: PROTOCOL_VERSION}});
  const client = await pending;
  assert.equal(client.protocolVersion, PROTOCOL_VERSION);
  client.dispose();
});

test('offline validation rejects missing assets and conflicting settings before creating workers', async () => {
  const {createNanoGo} = await import('../nanogo.mjs');
  let created = 0;
  const workerFactory = () => { created++; throw new Error('unexpected worker'); };
  for (const options of [
    {offline:true},
    {offline:true, workerFactory},
    {wasmBytes:'not bytes', workerFactory},
    {wasmBytes:new Uint8Array(), wasmURL:'x', workerFactory},
    {workerFactory, workerURL:'x'},
    {wasmExecProvided:true, wasmExecURL:'x', workerFactory},
    {wasmModule:{}, workerFactory}
  ]) await assert.rejects(createNanoGo(options), TypeError);
  assert.equal(created, 0);
});

test('offline missing runtime cleans up a supplied worker without posting init', async () => {
  const {createNanoGo} = await import('../nanogo.mjs');
  let terminated = 0, cleaned = 0;
  await assert.rejects(createNanoGo({offline:true, wasmBytes:new Uint8Array([0]), workerFactory: () => ({
    worker: {postMessage() {throw new Error('unexpected message');}, terminate() {terminated++;}},
    dispose() {cleaned++;}
  })}), /runtime/);
  assert.equal(terminated, 1);
  assert.equal(cleaned, 1);
});

test('unsupported protocol rejects startup and cleans factory resources once', async () => {
  const {createNanoGo} = await import('../nanogo.mjs');
  let terminated = 0, cleaned = 0;
  const worker = {postMessage() {}, terminate() {terminated++;}};
  const pending = createNanoGo({workerFactory: () => ({worker, dispose() {cleaned++;}})});
  const rejected = assert.rejects(pending, /protocol version/);
  worker.onmessage({data:{type:'ready', protocolVersion:999}});
  await rejected;
  assert.equal(terminated, 1);
  assert.equal(cleaned, 1);
});

test('legacy workers remain supported for URLs but cannot silently ignore supplied assets', async () => {
  const {createNanoGo} = await import('../nanogo.mjs');
  const worker = {postMessage() {}, terminate() {}};
  const pending = createNanoGo({wasmBytes:new Uint8Array([0]), workerFactory: () => worker});
  const rejected = assert.rejects(pending, /protocol version/);
  worker.onmessage({data:{type:'ready'}});
  await rejected;
});

test('inline factory owns blob URLs until disposal and releases failed construction', async t => {
  const {createInlineWorkerFactory, createNanoGo} = await import('../nanogo.mjs');
  const revoked = [], blobs = [];
  t.mock.method(URL, 'createObjectURL', blob => {blobs.push(blob); return 'blob:fixture';});
  t.mock.method(URL, 'revokeObjectURL', url => revoked.push(url));
  let worker;
  t.mock.method(globalThis, 'Worker', function(url) {
    assert.equal(url, 'blob:fixture');
    worker = {postMessage() {}, terminate() {}};
    return worker;
  });
  const factory = createInlineWorkerFactory({workerSource:'// worker', wasmExecSource:'// runtime'});
  const pending = createNanoGo({offline:true, wasmBytes:new Uint8Array([0]), workerFactory:factory});
  assert.match(await blobs[0].text(), /\/\/ runtime\n[\s\S]*\/\/ worker/);
  worker.onmessage({data:{type:'ready',protocolVersion:2}});
  const client = await pending;
  assert.deepEqual(revoked, []);
  client.dispose(); client.dispose();
  assert.deepEqual(revoked, ['blob:fixture']);
  t.mock.method(globalThis, 'Worker', function() {throw new Error('worker denied');});
  assert.throws(factory, /worker denied/);
  assert.deepEqual(revoked, ['blob:fixture','blob:fixture']);
});

test('input options require negotiated WASM capabilities', async t => {
  const {worker, pending} = await setup(t);
  worker.emit({type:'ready'});
  const client = await pending;
  await assert.rejects(client.run('source',{inputs:{x:1}}), /does not support inputs/);
  assert.equal(worker.sent.length, 1);
});

test('diagnostics remain available on errors without parsing text', async t => {
  const {worker, pending} = await setup(t);
  worker.emit({type:'ready',protocolVersion:2,capabilities:{hasStructuredResults:true}});
  const client = await pending;
  const run = client.run('source',{inputs:{x:1},limits:{maxSteps:10}});
  assert.deepEqual(worker.sent.at(-1).inputs, {x:1});
  const diagnostic = {code:'limit_exceeded',phase:'limit',message:'too many steps',limit:{resource:'steps',maximum:10,used:11}};
  const rejected = assert.rejects(run, error => error.diagnostic === diagnostic);
  worker.emit({type:'done',stats:{error:'too many steps',diagnostic}});
  await rejected;
});

test('compiled modules are forwarded without instantiating shared memory', async () => {
  const {createNanoGo}=await import('../nanogo.mjs');
  const wasmModule=await WebAssembly.compile(new Uint8Array([0,97,115,109,1,0,0,0]));
  let init;
  const worker={postMessage(message) {init=message;},terminate(){}};
  const pending=createNanoGo({offline:true,wasmModule,workerFactory:()=>({worker,wasmExecProvided:true})});
  assert.equal(init.wasmModule,wasmModule);
  assert.equal(init.wasmBytes,undefined);
  worker.onmessage({data:{type:'ready',protocolVersion:2}});
  (await pending).dispose();
});

test('factory cleanup keeps its receiver and cannot strand queue rejection', async () => {
  const {createNanoGo}=await import('../nanogo.mjs');
  let cleaned=0;
  const worker={postMessage(){},terminate(){throw new Error('termination failed');}};
  const handle={worker,dispose(){assert.equal(this,handle);cleaned++;throw new Error('cleanup failed');}};
  const pending=createNanoGo({workerFactory:()=>handle});
  worker.onmessage({data:{type:'ready',protocolVersion:2}});
  const client=await pending;
  const active=assert.rejects(client.run('a'),error=>error.diagnostic.code==='execution.disposed');
  const queued=assert.rejects(client.run('b'),/disposed/);
  client.dispose();client.dispose();
  await Promise.all([active,queued]);
  assert.equal(cleaned,1);
});
