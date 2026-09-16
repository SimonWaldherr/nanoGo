'use strict';

// Execute the actual shipping binary, not a separately compiled Go test binary.
// Usage: node scripts/smoke-wasm.cjs build/nanogo.wasm build/wasm_exec.js
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const { performance } = require('node:perf_hooks');

const wasmPath = path.resolve(process.argv[2] || 'web/nanogo.wasm');
const execPath = path.resolve(process.argv[3] || path.join(path.dirname(wasmPath), 'wasm_exec.js'));
if (!globalThis.crypto) globalThis.crypto = require('node:crypto').webcrypto;
require(execPath);

const messages = [];
globalThis.nanoGoPostMessage = message => messages.push(message);
globalThis.nanoGoPostConsole = (type, text) => messages.push({ type, text });
const ready = new Promise(resolve => { globalThis.nanoGoSignalReady = resolve; });
const watchdog = setTimeout(() => {
  console.error('WASM smoke test timed out');
  process.exit(1);
}, 30000);

function output() {
  return messages.filter(message => message.type === 'log').map(message => message.text).join('\n');
}

function run(source, expected, trace = false) {
  messages.length = 0;
  const result = JSON.parse(globalThis.nanoGoRun(source, trace, trace));
  assert.ok(!result.error, result.error);
  assert.ok(result.steps > 0, 'execution reports deterministic steps');
  assert.equal(output(), expected);
  return result;
}

async function main() {
  const start = performance.now();
  const go = new globalThis.Go();
  const { instance } = await WebAssembly.instantiate(fs.readFileSync(wasmPath), go.importObject);
  const running = go.run(instance);
  await Promise.race([ready, running.then(() => { throw new Error('Go exited before readiness'); })]);
  const startupMs = performance.now() - start;
  assert.equal(JSON.parse(globalThis.nanoGoVersion()).hasWorkspace, true);

  const loop = run(`package main
import "fmt"
func main() {
  sum := 0
  for i := 0; i < 10000; i++ { sum += i }
  fmt.Println(sum)
}`, '49995000');

  run(`package main
import ("fmt"; "encoding/gob"; "encoding/json"; "text/template"; "regexp")
func main() {
  data, err := gob.Encode("Grüße 🌍")
  if err != nil { panic(err) }
  value, err := gob.Decode(data)
  if err != nil { panic(err) }
  fmt.Println(value)
  text, err := json.Marshal(map[string]int{"answer": 42})
  if err != nil { panic(err) }
  fmt.Println(text)
  rendered, err := template.RenderString("Hello {{.Name}}", map[string]string{"Name": "nanoGo"})
  if err != nil { panic(err) }
  fmt.Println(rendered)
  pattern, err := regexp.Compile("^nano")
  if err != nil { panic(err) }
  fmt.Println(pattern.MatchString("nanoGo"))
}`, 'Grüße 🌍\n{"answer":42}\nHello nanoGo\ntrue');

  run(`package main
import "fmt"
func main() {
  x := 1000; y := 12345; x += y; x ^= y
  f := 1.25; step := 0.5; f += step; f *= 2.0
  fmt.Println(x, f)
}`, '1048 3.5', true);

  const traced = run('package main\nfunc main() { x := 7; ConsoleLog(x) }', '7', true);
  run(`package main
func main() {
  x := 8; n := -1
  defer func(){ ConsoleLog(recover()); ConsoleLog(x) }()
  x <<= n
}`, 'runtime error: negative shift amount\n8');
  assert.ok(traced.trace.length > 0);
  assert.ok(traced.profile.length > 0);

  messages.length = 0;
  const files = [
    { path: 'go.mod', source: 'module smoke.test/app\n' },
    { path: 'main.go', source: 'package main\nimport ("fmt"; "smoke.test/app/lib")\nfunc main() { fmt.Println(lib.Answer()) }' },
    { path: 'lib/answer.go', source: 'package lib\nfunc Answer() int { return 42 }' }
  ];
  const workspace = JSON.parse(globalThis.nanoGoRunWorkspace(JSON.stringify(files), 'smoke.test/app'));
  assert.ok(!workspace.error, workspace.error);
  assert.equal(output(), '42');
  assert.equal(workspace.workspace.files, 3);

  const tests = JSON.parse(globalThis.nanoGoTest(`package main
import "testing"
func TestAnswer(t *testing.T) { if 6 * 7 != 42 { t.Fatal("wrong answer") } }`));
  assert.ok(!tests.error, tests.error);
  assert.equal(tests.total, 1);
  assert.equal(tests.passed, true);

  const failure = JSON.parse(globalThis.nanoGoRun('package main\nfunc main() { panic("smoke failure") }'));
  assert.match(failure.error, /smoke failure/);
  run('package main\nfunc main() { ConsoleLog("after failure") }', 'after failure');

  clearTimeout(watchdog);
  console.log(`WASM smoke passed: startup ${startupMs.toFixed(1)} ms; loop ${loop.elapsedMs.toFixed(1)} ms, ${loop.steps} steps; codecs, templates, regexp, trace/profile, workspace, tests, error recovery`);
  process.exit(0); // The browser host intentionally leaves Go's event loop running.
}

main().catch(error => {
  clearTimeout(watchdog);
  console.error(error);
  process.exit(1);
});
