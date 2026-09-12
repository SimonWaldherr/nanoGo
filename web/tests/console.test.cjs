// Exercise the actual playground functions with a minimal DOM, without WASM.
const { test } = require('node:test');
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const source = fs.readFileSync(require('node:path').join(__dirname, '../playground.js'), 'utf8');
function setup() {
  const element = () => ({ dataset: {}, children: [], appendChild(child) {
    if (child.fragment) { for (const item of child.children) this.appendChild(item); return; }
    this.children.push(child); child.parentNode = this;
  }, removeChild(child) { this.children.splice(this.children.indexOf(child), 1); } });
  const logEl = Object.assign(element(), {scrollHeight: 1000, scrollTop: 900, clientHeight: 100});
  const context = vm.createContext({logEl, document: {hidden:false, addEventListener(){}, createElement:element,
    createDocumentFragment:()=>Object.assign(element(), {fragment:true})}, requestAnimationFrame(){},
    setTimeout(){}, highlightErrorLine(){}, postOutputToHost(){}, TextDecoder, Uint8Array,
    atob: s=>Buffer.from(s,'base64').toString('binary')});
  vm.runInContext(source.slice(source.indexOf('      let logLines = []'), source.indexOf('      function updateLogLinesFading()')), context);
  return {context, logEl, run: code=>vm.runInContext(code, context)};
}
test('burst retains only the newest 100 entries in order before rendering', () => {
  const {run,logEl}=setup();
  run('for(let i=0;i<10000;i++) logMessage(String(i));');
  assert.equal(run('pendingLogLines.length'),100);
  run('flushLogQueue()');
  assert.equal(logEl.children.length,100);
  assert.ok(logEl.children[0].textContent.endsWith(' 9900'));
  assert.ok(logEl.children[99].textContent.endsWith(' 9999'));
  run('logMessage("next"); flushLogQueue()');
  assert.equal(logEl.children.length,100);
  assert.ok(logEl.children[99].textContent.endsWith(' next'));
});
test('console preserves scroll position when reading previous output',()=>{
  const {run,logEl}=setup(); logEl.scrollTop=100;
  run('logMessage("new"); flushLogQueue()');
  assert.equal(logEl.scrollTop,100);
});
test('shared code round trips Unicode and literal percent signs',()=>{
  const {context,run}=setup();
  const start=source.indexOf('      function decodeCodeFromURL(');
  vm.runInContext(source.slice(start,source.indexOf('      function buildShareURL',start)),context);
  for(const input of ['fmt.Println("Grüße 🌍")','fmt.Printf("%d", 42)','100%']) {
    context.encoded=Buffer.from(input).toString('base64');
    assert.equal(run('decodeCodeFromURL(encoded)'),input);
  }
});

test('clearing output discards queued lines before the next paint',()=>{
  const {context,run}=setup();
  const start=source.indexOf('      function clearLog()');
  vm.runInContext(source.slice(start,source.indexOf('      // Focus management',start)),context);
  context.execTimeEl=null;
  context.document.getElementById=()=>({getContext:()=>({clearRect(){}})});
  run('logMessage("stale"); clearLog()');
  assert.equal(run('pendingLogLines.length'),0);
  assert.equal(run('pendingLogStart'),0);
});
