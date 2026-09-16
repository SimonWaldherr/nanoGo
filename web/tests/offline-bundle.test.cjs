const {test} = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const {execFileSync} = require('node:child_process');

test('standalone builder embeds assets safely and produces valid inline module syntax', t => {
  const dir = fs.mkdtempSync(path.join(os.tmpdir(), 'nanogo-offline-'));
  t.after(() => fs.rmSync(dir,{recursive:true,force:true}));
  const wasm = path.join(dir,'guest.wasm'), runtime = path.join(dir,'go.js'), output = path.join(dir,'offline.html');
  fs.writeFileSync(wasm,new Uint8Array([0,97,115,109]));
  fs.writeFileSync(runtime,'/* a runtime comment with </script> and Grüße */');
  execFileSync(process.execPath,[path.join(__dirname,'../../scripts/build-offline.cjs'),wasm,runtime,output]);
  const html = fs.readFileSync(output,'utf8');
  assert.equal((html.match(/<script\b/g) || []).length,1);
  assert.equal((html.match(/<\/script>/g) || []).length,1);
  assert.doesNotMatch(html, /<script[^>]+src=/);
  assert.match(html,/atob\("AGFzbQ=="\)/);
  assert.match(html,/runtime comment with \\u003c\/script>/);
  const body = html.slice(html.indexOf('<script type="module">') + '<script type="module">'.length,html.lastIndexOf('</script>'));
  const script = path.join(dir,'inline.mjs');
  fs.writeFileSync(script,body);
  execFileSync(process.execPath,['--check',script]);
});
