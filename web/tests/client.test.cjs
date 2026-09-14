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
  assert.deepEqual(worker.sent[0], {type:'init', wasmURL:'release.wasm', wasmExecURL:'go.js'});
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
