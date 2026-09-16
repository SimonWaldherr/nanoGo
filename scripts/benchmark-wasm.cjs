'use strict';

// Actual WASM benchmark, not a native proxy. JSON output can be saved as evidence.
// "fresh worker" includes instantiation/Go startup; module compilation is separate.
// Warm runs include parsing and fresh interpreter/package state on each request.
// Usage: node scripts/benchmark-wasm.cjs build/nanogo.wasm build/wasm_exec.js
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const {Worker} = require('node:worker_threads');
const {performance} = require('node:perf_hooks');
const wasmPath = path.resolve(process.argv[2] || 'web/nanogo.wasm');
const runtimePath = path.resolve(process.argv[3] || path.join(path.dirname(wasmPath), 'wasm_exec.js'));
const workerSource = `const {parentPort}=require('node:worker_threads');
globalThis.self=globalThis; globalThis.crypto=require('node:crypto').webcrypto;
globalThis.postMessage=m=>parentPort.postMessage(m);
globalThis.fetch=globalThis.importScripts=()=>{throw new Error('unexpected network');};
parentPort.on('message', data=>self.onmessage({data}));\n` +
  fs.readFileSync(runtimePath,'utf8') + '\n' + fs.readFileSync(path.join(__dirname,'../web/wasm_worker.js'),'utf8');
function workerFactory() {
  const nodeWorker = new Worker(workerSource,{eval:true});
  const worker = {postMessage:message=>nodeWorker.postMessage(message),terminate:()=>nodeWorker.terminate()};
  nodeWorker.on('message',data=>worker.onmessage?.({data}));
  nodeWorker.on('error',error=>worker.onerror?.({message:error.message}));
  return {worker,wasmExecProvided:true};
}
const workloads = {
  numericalLoop: {expected:'12497500',source:'package main\nfunc main(){sum:=0;for i:=0;i<5000;i++{sum+=i};ConsoleLog(sum)}'},
  nestedSlices: {expected:'2000',source:'package main\nfunc main(){points:=[][]float64{{0,0},{1,1}};total:=0.0;for i:=0;i<1000;i++{for _,point:=range points{total+=point[0]+point[1]}};ConsoleLog(total)}'},
  jsonRoundTrip: {expected:'ok',source:'package main\nimport "encoding/json"\nfunc main(){for i:=0;i<50;i++{data,err:=json.Marshal(map[string]any{"n":i,"values":[]int{1,2,3}});if err!=nil{panic(err)};var decoded map[string]any;if err:=json.Unmarshal(data,&decoded);err!=nil{panic(err)}};ConsoleLog("ok")}'},
};
function median(values) {return values.toSorted((a,b)=>a-b)[Math.floor(values.length/2)];}
async function main() {
  const {createNanoGo}=await import('../web/nanogo.mjs');
  const wasm=fs.readFileSync(wasmPath);
  const compileStart=performance.now();
  const wasmModule=await WebAssembly.compile(wasm);
  const report={runtime:process.version,platform:process.platform,arch:process.arch,wasmBytes:wasm.length,
    moduleCompileMs:performance.now()-compileStart,note:'Fresh worker is not a cold browser/process measurement. Warm requests still parse and execute with fresh interpreter state. Node WASM timings are not browser timings.',workloads:{}};
  for(const [name,program] of Object.entries(workloads)) {
    let output=[];
    const start=performance.now();
    const client=await createNanoGo({offline:true,wasmModule,workerFactory,onMessage:m=>{if(m.type==='log')output.push(m.text);}});
    const startupMs=performance.now()-start;
    try {
      const elapsed=[];
      for(let i=0;i<8;i++) {
        output=[];const before=performance.now();await client.run(program.source);elapsed.push(performance.now()-before);
        assert.deepEqual(output,[program.expected]);
      }
      report.workloads[name]={freshWorkerStartupMs:startupMs,firstExecutionMs:elapsed[0],warmExecutionMs:elapsed.slice(1),warmMedianMs:median(elapsed.slice(1))};
    } finally {client.dispose();}
  }
  console.log(JSON.stringify(report,null,2));
}
main().catch(error=>{console.error(error);process.exitCode=1;});
