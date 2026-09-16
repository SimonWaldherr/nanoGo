'use strict';

// Actual Go/WASM + browser SDK/worker, running in Node worker_threads with
// browser-like messaging. Browser security/lifecycle is checked separately.
// Usage: node scripts/smoke-offline.cjs build/nanogo.wasm build/wasm_exec.js
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const {Worker} = require('node:worker_threads');
const wasmPath = path.resolve(process.argv[2] || 'web/nanogo.wasm');
const runtimePath = path.resolve(process.argv[3] || path.join(path.dirname(wasmPath), 'wasm_exec.js'));
const prelude = `
const {parentPort} = require('node:worker_threads');
globalThis.self = globalThis;
globalThis.crypto = require('node:crypto').webcrypto;
globalThis.postMessage = message => parentPort.postMessage(message);
globalThis.fetch = globalThis.importScripts = () => {throw new Error('Unexpected network access');};
parentPort.on('message', data => self.onmessage({data}));
`;
const workerSource = prelude + '\n' + fs.readFileSync(runtimePath, 'utf8') + '\n' +
  fs.readFileSync(path.join(__dirname, '../web/wasm_worker.js'), 'utf8');
let created = 0, cleaned = 0;
function workerFactory() {
  created++;
  const nodeWorker = new Worker(workerSource, {eval:true});
  const worker = {
    postMessage: message => nodeWorker.postMessage(message),
    terminate: () => nodeWorker.terminate()
  };
  nodeWorker.on('message', data => worker.onmessage?.({data}));
  nodeWorker.on('error', error => worker.onerror?.({message:error.message}));
  return {worker,wasmExecProvided:true,dispose:() => {cleaned++;}};
}

async function main() {
  const {createNanoGo} = await import('../web/nanogo.mjs');
  const bytes = fs.readFileSync(wasmPath);
  const module = await WebAssembly.compile(bytes);
  const received = [];
  const options = {offline:true,workerFactory,initTimeoutMs:30000,onMessage:message => received.push(message)};
  for (const supplied of [{wasmBytes:bytes}, {wasmModule:module}]) {
    const client = await createNanoGo({...options,...supplied});
    try {
      assert.equal(client.protocolVersion, 2);
      for (let index=0; index<2; index++) {
        received.length = 0;
        await client.run('package main\nvar counter = 0\nfunc main() { counter++; ConsoleLog(counter) }');
        assert.deepEqual(received.filter(m => m.type === 'log').map(m => m.text), ['1']);
      }
      await assert.rejects(client.run('package main\nfunc main() { panic("expected failure") }'), /expected failure/);
      received.length = 0;
      await client.run('package main\nfunc main() { ConsoleLog("recovered") }');
      assert.equal(received.find(m => m.type === 'log').text, 'recovered');
      if (client.capabilities.hasStructuredResults) {
        const result = await client.run('package main\nimport "nanogo/host"\nfunc main() { v, err := host.Input("value"); if err != nil { panic(err) }; if err := host.Emit("answer", v); err != nil { panic(err) } }', {inputs:{value:{n:42}}});
        assert.deepEqual(result.stats.results, [{name:'answer',value:{n:42}}]);
        assert.equal(result.stats.resultsCommitted, true);
        await assert.rejects(client.run('package main\nfunc main() { host.Emit("partial",1); panic("after emit") }'), error => {
          assert.deepEqual(error.response.stats.results,[{name:'partial',value:1}]);
          assert.equal(error.response.stats.resultsCommitted,false);
          assert.equal(error.diagnostic.code,'runtime.panic');
          return true;
        });
        await assert.rejects(client.run('package main\nfunc main( {'), error => {
          assert.equal(error.diagnostic.code,'parse.syntax');
          assert.equal(error.diagnostic.location.file,'input.go');
          assert.equal(error.diagnostic.location.line,2);
          return true;
        });
        for (const [limits,body,code] of [
          [{maxSteps:10},'for {}','limit.steps'],
          [{maxOutputBytes:3},'ConsoleLog("1234")','limit.outputBytes'],
          [{maxAllocationUnits:2},'x:=make([]int,3);ConsoleLog(len(x))','limit.allocationUnits'],
          [{maxResultBytes:16},'host.Emit("x","x")','limit.resultBytes'],
        ]) await assert.rejects(client.run('package main\nfunc main(){'+body+'}',{limits}), error => {
          assert.equal(error.diagnostic.code,code);
          assert.equal(error.response.stats.resultsCommitted,false);
          assert.ok(error.diagnostic.limit.maximum>0);
          return true;
        });
        const recovered = await client.run('package main\nfunc main(){host.Emit("after limits",true)}');
        assert.equal(recovered.stats.resultsCommitted,true);
        assert.deepEqual(recovered.stats.results,[{name:'after limits',value:true}]);
        const files=[{path:'go.mod',source:'module offline.test/app\n'},
          {path:'main.go',source:'package main\nimport "nanogo/host"\nvar count=0\nfunc main(){count++;v,err:=host.Input("value");if err!=nil{panic(err)};host.Emit("value",v);host.Emit("count",count)}'}];
        for (const value of [3,9]) {
          const response=await client.runWorkspace(files,{modulePath:'offline.test/app',inputs:{value}});
          assert.equal(response.stats.resultsCommitted,true);
          assert.deepEqual(response.stats.results,[{name:'value',value},{name:'count',value:1}]);
        }
      }
    } finally { client.dispose(); }
  }
  const timed = await createNanoGo({...options,wasmModule:module,requestTimeoutMs:50});
  const pending = timed.run('package main\nfunc main() { for {} }');
  const queued = timed.format('package main\nfunc main() {}');
  await Promise.all([assert.rejects(pending,/request timed out/),assert.rejects(queued,/request timed out/)]);
  await assert.rejects(timed.run('later'), /request timed out/);
  timed.dispose();
  const restarted = await createNanoGo({...options,wasmModule:module});
  await restarted.run('package main\nfunc main() { ConsoleLog("fresh worker") }');
  restarted.dispose();
  await assert.rejects(createNanoGo({...options,wasmBytes:new Uint8Array([0,1,2,3])}), /WASM init failed/);
  assert.equal(created, cleaned);
  console.log('Offline WASM smoke passed: supplied bytes/module, no fetch/importScripts, fresh globals, inputs/results/completion, diagnostics/limits, workspace isolation, failure recovery, deadlines/queue rejection, restart, invalid binary, factory cleanup');
}

main().catch(error => {console.error(error);process.exitCode=1;});
