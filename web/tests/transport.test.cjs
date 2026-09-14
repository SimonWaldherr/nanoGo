const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const source = fs.readFileSync(require('node:path').join(__dirname, '../playground.js'), 'utf8');
function load(start, end, globals) {
  const context = vm.createContext(globals);
  const offset = source.indexOf(start);
  assert.ok(offset >= 0);
  vm.runInContext(source.slice(offset, source.indexOf(end, offset)), context);
  return code => vm.runInContext(code, context);
}

test('completion follows buffered iframe output without duplicates', async () => {
  const messages = [];
  const run = load('      function postToHost(', "      window.addEventListener('message'", {
    window: { parent: { postMessage: message => messages.push(message) } }
  });
  run(`postOutputToHost('first', 'output'); postOutputToHost('last', 'warn');
       postToHost({type:'nanogo:done'});`);
  await Promise.resolve();
  assert.deepEqual(JSON.parse(JSON.stringify(messages)), [
    { type: 'nanogo:output-batch', items: [{text:'first',kind:'output'}, {text:'last',kind:'warn'}] },
    { type: 'nanogo:done' }
  ]);
});

test('canvas adopts owned frames, rejects wrong sizes, and supports legacy arrays', () => {
  let paints = 0;
  const cells = new Uint8Array([1,2,3,4]);
  const run = load('      function applyCanvasFrame(', '      function startWasmWorker(', {
    Uint8Array, cells, _canvasCells: new Uint8Array(4), _scheduleCanvasRender: () => paints++
  });
  run('applyCanvasFrame(cells)');
  assert.equal(run('_canvasCells'), cells);
  run('applyCanvasFrame(new Uint8Array(2))');
  assert.equal(run('_canvasCells'), cells);
  assert.equal(paints, 1);
  run('applyCanvasFrame([4,3,2,1])');
  assert.deepEqual(Array.from(run('_canvasCells')), [4,3,2,1]);
});

test('unchanged workspace avoids rebuilding payload; edits and restart sync again', () => {
  let snapshots = 0;
  const messages = [];
  const run = load('      function postWorkspaceRequest(', '      function showWorkspaceResult(', {
    saveActiveWorkspaceSource() {}, workspacePayload() { snapshots++; return {files:[],modulePath:'demo'}; },
    workerWorkspaceRevision:-1, workspaceRevision:1, worker:{postMessage: message=>messages.push(message)}
  });
  run("postWorkspaceRequest('workspace-check'); postWorkspaceRequest('workspace-test'); postWorkspaceRequest('workspace-run');");
  assert.equal(snapshots, 1);
  assert.equal(messages.filter(m=>m.type==='workspace-sync').length, 1);
  run("workspaceRevision++; postWorkspaceRequest('workspace-run'); workerWorkspaceRevision=-1; postWorkspaceRequest('workspace-run');");
  assert.equal(snapshots, 3);
});
