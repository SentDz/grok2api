"use strict";

const readline = require("readline");
const vm = require("vm");

let cachedKey = "";
let cachedFn = null;

function compile(source) {
  const key = source;
  if (cachedFn && cachedKey === key) {
    return cachedFn;
  }
  const sandbox = {
    Math,
    Number,
    parseInt,
    parseFloat,
    isFinite,
    Infinity,
    NaN,
    JSON,
    Buffer,
    console: { log() {}, warn() {}, error() {} },
  };
  vm.createContext(sandbox);
  vm.runInContext(source, sandbox, { timeout: 2000, filename: "hot/hex.js" });
  if (typeof sandbox.computeHex !== "function") {
    throw new Error("hot JS 必须定义 computeHex(seed, paths)");
  }
  cachedKey = key;
  cachedFn = sandbox;
  return sandbox;
}

const rl = readline.createInterface({ input: process.stdin, terminal: false });
rl.on("line", (line) => {
  if (!line) {
    return;
  }
  try {
    const msg = JSON.parse(line);
    const sandbox = compile(String(msg.source || ""));
    sandbox.__seed = Buffer.from(String(msg.seed || ""), "base64");
    sandbox.__paths = msg.paths || [];
    const hex = vm.runInContext("computeHex(__seed, __paths)", sandbox, {
      timeout: 50,
      filename: "hot/hex.js#computeHex",
    });
    process.stdout.write(JSON.stringify({ ok: true, hex: String(hex) }) + "\n");
  } catch (err) {
    cachedFn = null;
    cachedKey = "";
    process.stdout.write(JSON.stringify({ ok: false, error: String(err && err.message ? err.message : err) }) + "\n");
  }
});
