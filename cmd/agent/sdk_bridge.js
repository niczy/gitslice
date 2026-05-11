#!/usr/bin/env node
// sdk_bridge.js — JSON-over-stdin/stdout bridge for Codex Agent SDK
// 
// Reads messages from stdin, runs the Codex SDK agent loop,
// writes responses to stdout. Falls back to a mock mode if
// @openai/codex is not installed.
//
// Usage: node sdk_bridge.js --workdir <dir> --agent codex|claude

const readline = require('readline');

const args = process.argv.slice(2);
const workdir = args[args.indexOf('--workdir') + 1] || process.cwd();
const agentType = args[args.indexOf('--agent') + 1] || 'codex';

let codexAgent;

try {
  const { Codex } = require('@openai/codex');
  codexAgent = new Codex({
    workdir,
    model: agentType,
  });
  console.error(`[bridge] Codex SDK initialized (${agentType})`);
} catch (e) {
  console.error(`[bridge] Codex SDK not installed, running in mock mode`);
  console.error(`[bridge] install with: npm install @openai/codex`);
}

const rl = readline.createInterface({
  input: process.stdin,
  output: process.stdout,
  terminal: false,
});

const pendingMessages = [];
let processing = false;

function writeMessage(msg) {
  process.stdout.write(JSON.stringify(msg) + '\n');
}

async function processInput(text) {
  if (codexAgent) {
    try {
      writeMessage({ type: 'output', text: 'Thinking...' });
      const result = await codexAgent.run({ prompt: text });
      writeMessage({ type: 'output_final', text: result.text || result });
    } catch (e) {
      writeMessage({ type: 'output_final', text: `Error: ${e.message}` });
    }
  } else {
    // Mock mode: echo with a simulated delay
    await new Promise(r => setTimeout(r, 500));
    writeMessage({ type: 'output', text: `Mock agent received: ${text}` });
    writeMessage({ type: 'output_final', text: `Mock response to: "${text}" — working in ${workdir}` });
  }
}

rl.on('line', async (line) => {
  try {
    const msg = JSON.parse(line);
    if (msg.type === 'input' && msg.text) {
      pendingMessages.push(msg.text);
      if (!processing) {
        processing = true;
        while (pendingMessages.length > 0) {
          const text = pendingMessages.shift();
          await processInput(text);
        }
        processing = false;
      }
    }
  } catch (e) {
    console.error(`[bridge] parse error: ${e.message}`);
  }
});

rl.on('close', () => {
  console.error('[bridge] stdin closed, exiting');
  process.exit(0);
});

console.error(`[bridge] ready (${agentType}, workdir=${workdir})`);
