const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const path = require('node:path');
function setup() {
  const elements = new Map();
  const document = {getElementById(id) {
    if (!elements.has(id)) elements.set(id, {value:'', textContent:'', hidden:false, disabled:false, focus(){this.focused=true;}});
    return elements.get(id);
  }};
  const copies = [];
  const context = vm.createContext({TextEncoder, document, window:{}, navigator:{clipboard:{async writeText(text){copies.push(text);}}}});
  vm.runInContext(fs.readFileSync(path.join(__dirname, '../data-panel.js'), 'utf8'), context);
  return {panel:context.window.nanoGoDataPanel, el:id=>document.getElementById(id), copies};
}
test('default options preserve legacy runs; supplied inputs and budgets are explicit', () => {
  const {panel,el}=setup();
  el('runInputs').value='{}';
  assert.equal(JSON.stringify(panel.readOptions()), '{}');
  el('runInputs').value='{"value":[1,2]}'; el('runStepLimit').value='1000000';
  assert.equal(JSON.stringify(panel.readOptions()), '{"inputs":{"value":[1,2]},"limits":{"maxSteps":1000000}}');
});
test('bad inputs fail locally and focus the editor', () => {
  const {panel,el}=setup();
  for (const input of ['{', 'null', '[]', '42', '{"value":1e400}', '{"x":'+ '['.repeat(66)+'0'+']'.repeat(66)+'}']) {
    el('runInputs').value=input;
    assert.throws(()=>panel.readOptions());
    assert.equal(el('runInputs').focused,true);
  }
  el('runInputs').value=' '.repeat(1024*1024+1);
  assert.throws(()=>panel.readOptions(), /1 MiB/);
});
test('partial events remain separate from logs and cannot be copied as committed', async () => {
  const {panel,el,copies}=setup();
  panel.start();
  assert.equal(el('copyResultsBtn').disabled,true);
  const diagnostic={code:'runtime.panic',phase:'runtime',message:'bad',location:{file:'main.go',line:4,column:2}};
  panel.finish({stats:{results:[{name:'draft',value:'<script>bad</script>'}],resultsCommitted:false,diagnostic,error:'bad'}});
  assert.match(el('resultStatus').textContent,/Partial/);
  assert.match(el('resultData').textContent,/<script>/);
  assert.match(el('runDiagnostic').textContent,/runtime.panic/);
  await el('copyResultsBtn').onclick();
  assert.equal(JSON.parse(copies[0]).committed,false);
  panel.start();
  assert.equal(el('runDiagnostic').textContent,'');
  assert.equal(el('resultData').textContent,'');
  panel.finish({stats:{results:[{name:'answer',value:42}],resultsCommitted:true}});
  assert.match(el('resultStatus').textContent,/Completed/);
});
test('absent stats, terminal errors and Stop never display successful results', () => {
  const {panel,el}=setup();
  panel.finish({stats:null});
  assert.match(el('resultStatus').textContent,/unavailable/i);
  panel.finish({error:'startup failed',stats:{resultsCommitted:true,results:[]}});
  assert.match(el('resultStatus').textContent,/Failed/);
  panel.start(); panel.stop('Stopped');
  assert.equal(el('resultStatus').textContent,'Stopped');
  assert.equal(el('copyResultsBtn').disabled,true);
});

test('playground completion uses terminal failure even without stats', () => {
  const source = fs.readFileSync(path.join(__dirname, '../playground.js'), 'utf8');
  const start = source.indexOf('      function finishRun(');
  const messages=[], states=[], outcomes=[];
  const context=vm.createContext({
    dataPanel:{finish:message=>outcomes.push(message)},
    logMessage:(message,type)=>messages.push({message,type}),
    setStatus:(message,type)=>states.push({message,type}),
    execTimeEl:null, updateRunStats(){}, activeRunSource:'', selectedSymbol:null,
    applyBreakpointHits(){}, renderWorkspaceTabs(){}, workspaceRunBtn:null
  });
  vm.runInContext(source.slice(start,source.indexOf('      function handleTestResult',start)),context);
  vm.runInContext('finishRun(1, null, "bad input", {code:"parse.syntax"})',context);
  assert.equal(messages[0].type,'error');
  assert.match(messages[0].message,/failed/);
  assert.equal(states[0].type,'error');
  assert.equal(outcomes[0].diagnostic.code,'parse.syntax');
});

test('worker restart keeps edited source and inputs and does not autorun again', async () => {
  const source=fs.readFileSync(path.join(__dirname, '../playground.js'), 'utf8');
  const ready=source.slice(source.indexOf("            case 'ready':"), source.indexOf("            case 'log':", source.indexOf("            case 'ready':")));
  let loads=0,runs=0,readyMessages=0;
  const context=vm.createContext({
    sourceInitialized:false, setStatus(){},runBtn:{},
    astBtn:null,callGraphBtn:null,benchBtn:null,heatmapToggle:null,workspaceCheckBtn:null,
    workspaceInspectBtn:null,workspaceRunBtn:null,workspaceMetaEl:null,exampleCountBadgeEl:null,
    logMessage(){},fillExamples(){},introduceExamplesDrawer(){},
    async loadCodeFromURL(){loads++;},setupEmbedPopout(){},
    postToHost(){readyMessages++;},wantAutorun:true,runCode(){runs++;}
  });
  vm.runInContext('function ready(m) { switch(m.type) {'+ready+'} }',context);
  vm.runInContext('ready({type:"ready"})',context);
  await Promise.resolve();
  vm.runInContext('ready({type:"ready"})',context);
  await Promise.resolve();
  assert.equal(loads,1);
  assert.equal(runs,1);
  assert.equal(readyMessages,2);
});
