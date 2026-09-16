'use strict';

// Bundle local SDK assets into one HTML file. No dependencies or network.
// Usage: node scripts/build-offline.cjs [WASM] [wasm_exec.js] [output.html]
const fs = require('node:fs');
const path = require('node:path');
const wasmPath = path.resolve(process.argv[2] || 'web/nanogo.wasm');
const runtimePath = path.resolve(process.argv[3] || path.join(path.dirname(wasmPath), 'wasm_exec.js'));
const outputPath = path.resolve(process.argv[4] || 'build/offline.html');
const sdk = fs.readFileSync(path.join(__dirname, '../web/nanogo.mjs'), 'utf8');
const runtime = fs.readFileSync(runtimePath, 'utf8');
const worker = fs.readFileSync(path.join(__dirname, '../web/wasm_worker.js'), 'utf8');
const wasm = fs.readFileSync(wasmPath).toString('base64');
// JSON literals inside HTML must escape '<', including any </script> in assets.
const literal = value => JSON.stringify(value).replace(/</g, '\\u003c');
const html = `<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width">
<title>nanoGo offline</title>
<style>body{font:16px system-ui;max-width:58rem;margin:3rem auto;padding:0 1rem;color:#192334;background:#f7f8fa}textarea{box-sizing:border-box;width:100%;height:17rem;padding:1rem;font:15px monospace}button,select{font:inherit;padding:.5rem .8rem;margin:.7rem .4rem .7rem 0}pre{white-space:pre-wrap;overflow-wrap:anywhere;background:white;padding:1rem;min-height:5rem}label{display:block}#status{font-weight:600}</style>
<h1>nanoGo, completely offline</h1>
<p>This file contains its own interpreter, worker, Go runtime, and SDK. It can be opened from disk. No CDN or server is required.</p>
<label for="source">Go program</label><textarea id="source" spellcheck="false">package main

import "fmt"

func main() {
    sum := 0
    for i := 1; i &lt;= 10; i++ { sum += i }
    fmt.Println("Hello from offline nanoGo:", sum)
}</textarea>
<label for="asset">Startup asset</label><select id="asset"><option value="bytes">Supplied WASM bytes</option><option value="module">Compiled WebAssembly.Module</option></select>
<button id="run">Run</button><button id="restart">Restart worker</button><button id="verify">Verify offline lifecycle</button>
<p id="status" role="status">Ready to initialize.</p><pre id="output" aria-live="polite"></pre>
<script type="module">
${sdk}
const bytes = Uint8Array.from(atob(${literal(wasm)}), character => character.charCodeAt(0));
const runtimeSource = ${literal(runtime)};
const workerSource = ${literal(worker)};
const status = document.getElementById('status');
const output = document.getElementById('output');
const asset = document.getElementById('asset');
const source = document.getElementById('source');
let client, compiledModule;
let networkAttempts = 0;
const denyNetwork = () => { networkAttempts++; throw new Error('Unexpected network request'); };
// These guards make this runnable example also a check for accidental requests.
// Creating local blob workers does not use fetch/importScripts.
globalThis.fetch = denyNetwork;
globalThis.XMLHttpRequest = class { constructor() { denyNetwork(); } };
const guardSource = "globalThis.fetch = globalThis.importScripts = () => { throw new Error('Unexpected worker network request'); };\\n";
const workerFactory = createInlineWorkerFactory({wasmExecSource:guardSource + runtimeSource,workerSource});
function message(message) {
  if (message.type === 'log' || message.type === 'warn' || message.type === 'error') output.textContent += (message.text || '') + '\\n';
}
async function start(requestTimeoutMs = 10000) {
  if (client) client.dispose();
  client = null;
  const supplied = asset.value === 'module' ? {wasmModule:compiledModule ||= await WebAssembly.compile(bytes)} : {wasmBytes:bytes};
  client = await createNanoGo({offline:true,workerFactory,...supplied,requestTimeoutMs,onMessage:message});
  return client;
}
async function action(body) {
  for (const button of document.querySelectorAll('button')) button.disabled = true;
  try { await body(); }
  catch (error) { status.textContent = 'Failed: ' + error.message; output.textContent += JSON.stringify(error.diagnostic || error.response || error.message, null, 2) + '\\n'; }
  finally { for (const button of document.querySelectorAll('button')) button.disabled = false; }
}
document.getElementById('run').onclick = () => action(async () => {
  output.textContent = ''; status.textContent = 'Running…';
  if (!client) await start();
  const result = await client.run(source.value);
  if (result.stats?.results) output.textContent += JSON.stringify(result.stats.results, null, 2) + '\\n';
  status.textContent = 'Completed; protocol ' + client.protocolVersion + '; network attempts: ' + networkAttempts;
});
document.getElementById('restart').onclick = () => action(async () => {
  await start(); status.textContent = 'Fresh worker ready; network attempts: ' + networkAttempts;
});
asset.onchange = () => { client?.dispose(); client = null; };
document.getElementById('verify').onclick = () => action(async () => {
  output.textContent = ''; const passed = [];
  const check = (condition, name) => { if (!condition) throw new Error(name); passed.push(name); output.textContent += 'PASS ' + name + '\\n'; };
  for (const mode of ['bytes','module']) {
    asset.value = mode; await start();
    const program = 'package main\\nvar n = 0\\nfunc main() { n++; ConsoleLog(n) }';
    for (let i=0;i<2;i++) await client.run(program);
    check(output.textContent.endsWith('1\\n1\\n'), mode + ': repeated executions have fresh globals');
    let failed = false;
    try { await client.run('package main\\nfunc main() { panic("expected") }'); } catch { failed = true; }
    check(failed, mode + ': guest panic rejects');
    await client.run('package main\\nfunc main() { ConsoleLog("after failure") }');
    check(output.textContent.endsWith('after failure\\n'), mode + ': execution after failure');
    if (client.capabilities.hasStructuredResults) {
      const result = await client.run('package main\\nfunc main() { v, err := host.Input("value"); if err != nil { panic(err) }; host.Emit("value",v) }', {inputs:{value:42}});
      check(result.stats.resultsCommitted && result.stats.results[0].value === 42, mode + ': structured inputs and committed output');
      let partial;
      try { await client.run('package main\\nfunc main() { host.Emit("partial",7); panic("after result") }'); } catch (error) { partial = error; }
      check(partial?.response?.stats?.resultsCommitted === false && partial.response.stats.results[0].value === 7 && partial.diagnostic.code === 'runtime.panic', mode + ': partial output retains failure diagnostic');
    }
  }
  await start(50);
  const active = client.run('package main\\nfunc main() { for {} }');
  const queued = client.format('package main\\nfunc main() {}');
  const outcomes = await Promise.allSettled([active,queued]);
  check(outcomes.every(result => result.status === 'rejected' && /request timed out/.test(result.reason.message)), 'deadline rejects active and queued requests');
  await start(); await client.run('package main\\nfunc main() { ConsoleLog("restarted") }');
  check(output.textContent.endsWith('restarted\\n'), 'restart after termination');
  check(networkAttempts === 0, 'no network requests');
  const resources = performance.getEntriesByType('resource').filter(entry => /^https?:/.test(entry.name));
  check(resources.length === 0, 'no HTTP resource loads');
  status.textContent = 'Offline verification passed: ' + passed.length + ' checks; network attempts: ' + networkAttempts;
});
window.addEventListener('pagehide', () => client?.dispose());
</script></html>`;
fs.mkdirSync(path.dirname(outputPath), {recursive:true});
fs.writeFileSync(outputPath, html);
console.log(`Wrote ${outputPath} (${Buffer.byteLength(html)} bytes). Open directly in a browser; all assets are embedded.`);
