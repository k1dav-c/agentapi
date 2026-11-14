/**
 * Measures what an AgentAPI costs to run, for the tables in docs/cli.md ("Resource usage"). Linux
 * only: it reads /proc.
 *
 *   bun scripts/measure-resources.mjs                       # native-demo only
 *   bun scripts/measure-resources.mjs --agents claude,codex,pi,native-demo
 *   bun scripts/measure-resources.mjs --long 0,250,1000 --long-kb 100
 *
 * Per agent it starts one AgentAPI with one chat (an SSE client) connected and records AgentAPI's
 * and the agent's resident memory (the agent's processes together), AgentAPI's CPU over an idle
 * minute, and the CPU seconds of each of three turns of about 200 words without tools, for
 * AgentAPI and for the agent's processes. With --long it builds a native-demo conversation in a
 * state database and, at each checkpoint (a number of tasks), restarts AgentAPI from it and records
 * the database's size, AgentAPI's memory and the size of the first /events?sync=1 replay.
 *
 * Every AgentAPI runs isolated: an empty config file of its own (no ~/.agentapi/config.yaml, no hub,
 * no provider ledger there), --registry=false, --bind 127.0.0.1 on a free port, a scratch working
 * directory, and an environment of HOME, PATH and LANG only, so the agent keeps the user's login
 * under the real HOME. Real agents send their turns to the user's accounts, so they run only when
 * --agents names them; the script prints what it will run first.
 *
 * Options:
 *   --agents LIST     claude, codex, pi and native-demo, comma-separated (default native-demo)
 *   --binary PATH     the agentapi to measure (default out/agentapi, else agentapi on PATH)
 *   --turns N         turns per agent (default 3)
 *   --idle SECONDS    the idle measurement (default 60)
 *   --long LIST       checkpoints of the long conversation in tasks, e.g. 0,250,1000 (default none)
 *   --long-kb KB      the size of each of its prompts (default 90)
 *   --path PATH       PATH for AgentAPI and the agent (default ~/.local/bin:/usr/local/bin:/usr/bin:/bin)
 *   --json FILE       also write the JSON result to FILE
 *   --keep            keep the scratch directory (it is kept anyway when a run fails)
 *
 * It prints the JSON result, then the Markdown tables; progress goes to stderr.
 */
import {spawn, execFileSync} from 'node:child_process';
import {existsSync, mkdirSync, openSync, readFileSync, readdirSync, rmSync, statSync, writeFileSync, closeSync, mkdtempSync} from 'node:fs';
import net from 'node:net';
import os from 'node:os';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

if (process.platform !== 'linux') {
  console.error('measure-resources reads /proc: Linux only.');
  process.exit(2);
}

const repo = fileURLToPath(new URL('..', import.meta.url));
const home = os.homedir();

const options = {agents: 'native-demo', turns: '3', idle: '60', long: '', 'long-kb': '90', path: `${home}/.local/bin:/usr/local/bin:/usr/bin:/bin`};
const flags = new Set();
for (let i = 2; i < process.argv.length; i++) {
  const arg = process.argv[i];
  const name = arg.replace(/^--/, '').split('=')[0];
  if (arg === '--keep' || arg === '--help' || arg === '-h') {
    flags.add(arg.replace(/^-+/, ''));
  } else if (arg.startsWith('--') && ['agents', 'binary', 'turns', 'idle', 'long', 'long-kb', 'path', 'json'].includes(name)) {
    options[name] = arg.includes('=') ? arg.slice(arg.indexOf('=') + 1) : process.argv[++i];
  } else {
    console.error(`unknown argument ${arg}; see the header of scripts/measure-resources.mjs`);
    process.exit(2);
  }
}
if (flags.has('help') || flags.has('h')) {
  console.log(readFileSync(fileURLToPath(import.meta.url), 'utf8').split('*/')[0].replace(/^\/\*\*\n?| ?\* ?/gm, ''));
  process.exit(0);
}

const AGENTS = {
  claude: {name: 'Claude Code', program: 'claude'},
  codex: {name: 'Codex', program: 'codex'},
  pi: {name: 'Pi', program: 'pi'},
  'native-demo': {name: '`native-demo` (no agent process)', program: null},
};
const agents = options.agents.split(',').map(s => s.trim()).filter(Boolean);
for (const a of agents) {
  if (!AGENTS[a]) {
    console.error(`unknown agent ${a}: use ${Object.keys(AGENTS).join(', ')}`);
    process.exit(2);
  }
}
const turns = Number(options.turns);
const idleSeconds = Number(options.idle);
const checkpoints = options.long ? options.long.split(',').map(Number) : [];
const longKB = Number(options['long-kb']);
if (!(turns >= 1) || !(idleSeconds > 0) || checkpoints.some(n => !(n >= 0)) || !(longKB > 0)) {
  console.error('--turns, --idle, --long and --long-kb take positive numbers');
  process.exit(2);
}

const env = {HOME: home, PATH: options.path, LANG: process.env.LANG || 'C.UTF-8'};
const which = program => {
  for (const dir of env.PATH.split(':')) {
    const full = path.join(dir, program);
    try {
      if (statSync(full).isFile()) return full;
    } catch {}
  }
  return null;
};
const binary = path.resolve(options.binary || (existsSync(path.join(repo, 'out', 'agentapi')) ? path.join(repo, 'out', 'agentapi') : which('agentapi') || 'agentapi'));
if (!existsSync(binary)) {
  console.error(`no agentapi at ${binary}: build it (make build) or pass --binary`);
  process.exit(2);
}

// The turns. native-demo acts on keywords (lib/native/demo/demo.go, words["…"]), so neither its
// prompt nor the long conversation's filler may contain one.
const PROMPT = "Write about 200 words, in plain prose, on why version control matters for a small team. Don't use any tools and don't read or write any files.";
const demoKeywords = (() => {
  const known = ['approve', 'ask', 'cold', 'crash', 'crashloop', 'delegate', 'gateway', 'limit', 'login', 'long', 'plan', 'secret'];
  try {
    const source = readFileSync(path.join(repo, 'lib', 'native', 'demo', 'demo.go'), 'utf8');
    return [...new Set([...known, ...[...source.matchAll(/words\["([a-z]+)"\]/g)].map(m => m[1])])];
  } catch {
    return known;
  }
})();
const keywordsIn = text => {
  const words = new Set(text.toLowerCase().split(/[^\p{L}]+/u));
  return demoKeywords.filter(k => words.has(k));
};
if (keywordsIn(PROMPT).length) throw new Error(`the prompt has native-demo keywords: ${keywordsIn(PROMPT)}`);
// Vowel-free words, which no keyword is.
const filler = n => {
  const letters = 'bcdfghjkmnpqrstvwxz';
  let out = '';
  while (out.length < n) {
    const len = 3 + Math.floor(Math.random() * 7);
    for (let i = 0; i < len; i++) out += letters[Math.floor(Math.random() * letters.length)];
    out += ' ';
  }
  return out.slice(0, n);
};

// What it will run, before anything runs.
const log = (...args) => console.error(...args);
const real = agents.filter(a => AGENTS[a].program);
log(`agentapi: ${binary}`);
log(`environment: HOME=${env.HOME} PATH=${env.PATH} LANG=${env.LANG}`);
for (const a of agents) {
  const program = AGENTS[a].program;
  log(`- ${a}: ${turns} turns of the prompt "${PROMPT.slice(0, 60)}…" after ${idleSeconds} s idle` +
    (program ? `, with ${which(program) || `${program} (not found on PATH)`}` : ''));
}
if (checkpoints.length) log(`- long conversation: native-demo, ${Math.max(...checkpoints)} tasks of ${longKB} KB prompts, measured at ${checkpoints.join(', ')}`);
if (real.length) log(`${real.join(', ')} will send ${turns} turns each to your own account${real.length > 1 ? 's' : ''}.`);
for (const a of real) {
  if (!which(AGENTS[a].program)) {
    console.error(`${AGENTS[a].program} isn't on PATH (${env.PATH}); pass --path`);
    process.exit(2);
  }
}

const scratch = mkdtempSync(path.join(os.tmpdir(), 'agentapi-measure-'));
log(`scratch directory: ${scratch}`);

// --- processes ---

const servers = new Set();

const freePort = () => new Promise((resolve, reject) => {
  const s = net.createServer();
  s.once('error', reject);
  s.listen(0, '127.0.0.1', () => {
    const {port} = s.address();
    s.close(() => resolve(port));
  });
});

// startServer starts an AgentAPI in a process group of its own, so that stopping it reaches the
// agent it started too.
const startServer = async (dir, agent, extra = []) => {
  mkdirSync(path.join(dir, 'work'), {recursive: true});
  const config = path.join(dir, 'config.yaml');
  if (!existsSync(config)) writeFileSync(config, '');
  const port = await freePort();
  const args = ['server', '--port', String(port), '--bind', '127.0.0.1', '--config', config, '--registry=false', ...extra, '--', agent];
  const out = openSync(path.join(dir, 'server.log'), 'a');
  const child = spawn(binary, args, {cwd: path.join(dir, 'work'), env, stdio: ['ignore', out, out], detached: true});
  closeSync(out);
  const exited = new Promise(resolve => child.once('exit', resolve));
  const server = {child, port, pid: child.pid, dir, exited, done: false};
  exited.then(() => { server.done = true; });
  servers.add(server);
  const deadline = Date.now() + 120_000;
  while (Date.now() < deadline) {
    if (server.done) throw new Error(`agentapi exited at startup; see ${dir}/server.log`);
    try {
      if ((await get(server, '/status')).status === 'stable') return server;
    } catch {}
    await sleep(250);
  }
  throw new Error(`${agent} wasn't ready after 120 s; see ${dir}/server.log`);
};

const signalGroup = (server, signal) => {
  try {
    process.kill(-server.pid, signal);
  } catch {}
};

const stopServer = async server => {
  if (!server.done) {
    signalGroup(server, 'SIGTERM');
    const timer = setTimeout(() => signalGroup(server, 'SIGKILL'), 15_000);
    await server.exited;
    clearTimeout(timer);
  }
  // The agent's processes may outlive AgentAPI by a moment.
  signalGroup(server, 'SIGKILL');
  servers.delete(server);
};

const stopAll = () => Promise.all([...servers].map(stopServer));
let failed = false;
process.on('exit', () => {
  for (const s of servers) signalGroup(s, 'SIGKILL');
});
for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
  process.on(signal, async () => {
    log(`\n${signal}: stopping the servers; the scratch directory stays: ${scratch}`);
    await stopAll();
    process.exit(130);
  });
}

// --- HTTP ---

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const url = (server, p) => `http://127.0.0.1:${server.port}${p}`;
const get = async (server, p) => {
  const res = await fetch(url(server, p), {signal: AbortSignal.timeout(30_000)});
  if (!res.ok) throw new Error(`GET ${p}: ${res.status}`);
  return res.json();
};
const send = async (server, content) => {
  const res = await fetch(url(server, '/message'), {
    method: 'POST', headers: {'Content-Type': 'application/json'},
    body: JSON.stringify({type: 'user', content}), signal: AbortSignal.timeout(60_000),
  });
  if (!res.ok) throw new Error(`POST /message: ${res.status} ${await res.text()}`);
};

// turn sends a message and waits until the turn it starts has ended: /status's turn has moved past
// the one before it, the status is stable and nothing waits in the queue (stable alone may come
// before the message even left the queue).
const turn = async (server, content, limit = 300_000) => {
  const before = (await get(server, '/status')).turn ?? 0;
  await send(server, content);
  const deadline = Date.now() + limit;
  while (Date.now() < deadline) {
    if (server.done) throw new Error(`agentapi exited during a turn; see ${server.dir}/server.log`);
    const st = await get(server, '/status');
    if ((st.turn ?? 0) > before && st.status === 'stable' && (await get(server, '/queue')).messages.length === 0) return;
    await sleep(200);
  }
  throw new Error(`the turn didn't end within ${limit / 1000} s; see ${server.dir}/server.log`);
};

// replayBytes reads the first full /events?sync=1 replay: up to the end of the event that carries
// id:, the replay's last; a replay at seq 0 has no id, so it ends where the stream goes quiet.
const replayBytes = async server => {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 120_000);
  try {
    const res = await fetch(url(server, '/events?sync=1'), {headers: {'Accept-Encoding': 'identity'}, signal: controller.signal});
    const reader = res.body.getReader();
    const decoder = new TextDecoder();
    let text = '';
    let bytes = 0;
    let seq = null;
    for (;;) {
      const quiet = seq === 0 ? sleep(1000).then(() => 'quiet') : new Promise(() => {});
      const next = await Promise.race([reader.read(), quiet]);
      if (next === 'quiet' || next.done) return bytes;
      bytes += next.value.length;
      text += decoder.decode(next.value, {stream: true});
      if (seq === null) {
        const m = text.match(/^event: session_sync\ndata: (.*)\n/m);
        if (m) seq = JSON.parse(m[1]).seq;
      }
      const id = text.search(/(^|\n)id: /);
      if (id >= 0) {
        const end = text.indexOf('\n\n', id + 1);
        if (end >= 0) return Buffer.byteLength(text.slice(0, end + 2));
      }
    }
  } catch (err) {
    if (controller.signal.aborted) throw new Error('the replay took over 120 s');
    throw err;
  } finally {
    clearTimeout(timer);
    controller.abort();
  }
};

// --- /proc ---

const HZ = Number(execFileSync('getconf', ['CLK_TCK']).toString().trim());
const procStat = pid => {
  const fields = readFileSync(`/proc/${pid}/stat`, 'utf8').split(') ').pop().split(' ');
  // state is fields[0]: ppid 1, utime 11, stime 12, cutime 13, cstime 14.
  return {ppid: Number(fields[1]), cpu: (Number(fields[11]) + Number(fields[12])) / HZ,
    children: (Number(fields[13]) + Number(fields[14])) / HZ};
};
const cpuOf = pid => procStat(pid).cpu;
const rssMiB = pid => {
  const m = readFileSync(`/proc/${pid}/status`, 'utf8').match(/^VmRSS:\s+(\d+) kB/m);
  return m ? Number(m[1]) / 1024 : 0;
};
const descendants = pid => {
  const kids = new Map();
  for (const name of readdirSync('/proc')) {
    if (!/^\d+$/.test(name)) continue;
    try {
      const {ppid} = procStat(name);
      if (!kids.has(ppid)) kids.set(ppid, []);
      kids.get(ppid).push(Number(name));
    } catch {}
  }
  const out = [];
  const todo = [...(kids.get(pid) || [])];
  while (todo.length) {
    const p = todo.pop();
    out.push(p);
    todo.push(...(kids.get(p) || []));
  }
  return out;
};
const sum = (pids, f) => pids.reduce((total, p) => {
  try {
    return total + f(p);
  } catch {
    return total; // it exited
  }
}, 0);
// The agent's CPU: its processes' own and that of the children they reaped.
const treeCPU = pids => sum(pids, p => {
  const s = procStat(p);
  return s.cpu + s.children;
});

// --- the measurements ---

const version = (program, args = ['--version']) => {
  try {
    return execFileSync(program, args, {env, timeout: 30_000, stdio: ['ignore', 'pipe', 'pipe']}).toString().trim().split('\n')[0];
  } catch (err) {
    return `unknown (${err.message.split('\n')[0]})`;
  }
};

const measureAgent = async agent => {
  const dir = path.join(scratch, agent);
  log(`${agent}: starting`);
  const server = await startServer(dir, agent);
  const chat = new AbortController();
  // One chat connected: a reader of /events that drops what it reads.
  fetch(url(server, '/events'), {signal: chat.signal}).then(async res => {
    for (const reader = res.body.getReader(); !(await reader.read()).done;);
  }).catch(() => {});
  try {
    await sleep(5000);
    log(`${agent}: idle for ${idleSeconds} s`);
    const c0 = cpuOf(server.pid);
    const t0 = performance.now();
    await sleep(idleSeconds * 1000);
    const idlePct = (cpuOf(server.pid) - c0) / ((performance.now() - t0) / 1000) * 100;
    const agentPids = descendants(server.pid);
    const result = {agent, idle_cpu_pct: idlePct, agentapi_mib_idle: rssMiB(server.pid),
      agent_mib_idle: agent === 'native-demo' ? null : sum(agentPids, rssMiB), agent_processes: agentPids.length, turns: []};
    for (let n = 0; n < turns; n++) {
      log(`${agent}: turn ${n + 1} of ${turns}`);
      const before = descendants(server.pid);
      const a0 = cpuOf(server.pid);
      const g0 = treeCPU(before);
      const start = performance.now();
      await turn(server, PROMPT);
      const seconds = (performance.now() - start) / 1000;
      await sleep(2000);
      const after = descendants(server.pid);
      result.turns.push({seconds, agentapi_cpu_s: cpuOf(server.pid) - a0,
        agent_cpu_s: agent === 'native-demo' ? null : treeCPU(after) - g0,
        agentapi_mib: rssMiB(server.pid), agent_mib: agent === 'native-demo' ? null : sum(after, rssMiB)});
      if (n < turns - 1) await sleep(5000);
    }
    const messages = (await get(server, '/messages')).messages;
    result.last_reply_words = messages.at(-1).content.split(/\s+/).filter(Boolean).length;
    return result;
  } finally {
    chat.abort();
    await stopServer(server);
  }
};

const measureLong = async () => {
  const dir = path.join(scratch, 'long');
  const db = path.join(dir, 'state.db');
  const dbSize = () => ['', '-wal'].reduce((total, s) => total + (existsSync(db + s) ? statSync(db + s).size : 0), 0);
  const rows = [];
  let server = await startServer(dir, 'native-demo', ['--state-db', db]);
  try {
    let done = 0;
    for (const check of [...checkpoints].sort((a, b) => a - b)) {
      for (; done < check; done++) {
        const text = `Task ${done}: summarize this text.\n\n${filler(longKB * 1024)}`;
        if (keywordsIn(text).length) throw new Error(`filler with native-demo keywords: ${keywordsIn(text)}`);
        await turn(server, text);
        if ((done + 1) % 50 === 0) log(`long conversation: ${done + 1} tasks`);
      }
      await stopServer(server);
      server = await startServer(dir, 'native-demo', ['--state-db', db]);
      await sleep(3000);
      const replay = await replayBytes(server);
      await sleep(2000);
      const row = {tasks: done, db_mb: dbSize() / 1e6, agentapi_mib: rssMiB(server.pid), replay_kb: replay / 1024};
      log(`long conversation: ${JSON.stringify(row)}`);
      rows.push(row);
    }
    return {prompt_kb: longKB, checkpoints: rows};
  } finally {
    await stopServer(server);
  }
};

// --- output ---

const range = (values, digits, unit) => {
  const v = values.filter(x => x != null);
  if (!v.length) return '';
  const lo = Math.min(...v).toFixed(digits);
  const hi = Math.max(...v).toFixed(digits);
  return `${lo === hi ? lo : `${lo}–${hi}`}${unit}`;
};
const size = kb => kb >= 1024 ? `${(kb / 1024).toFixed(1)} MB` : `${kb.toFixed(1)} KB`;

const markdown = result => {
  const lines = [];
  const m = result.machine;
  const versions = Object.entries(result.agent_versions).map(([a, v]) => `${AGENTS[a].name} ${v}`);
  lines.push(`Measured on ${result.date} with AgentAPI ${result.agentapi_version} (a ${result.binary_mib.toFixed(1)} MiB binary)` +
    `${versions.length ? `, ${versions.join(', ')}` : ''}, on ${m.os} with a ${m.cores}-core ${m.cpu} and one chat (an SSE client) connected; ` +
    `${result.turns} turns per agent, idle CPU over ${result.idle_seconds} s.`);
  if (result.agents.length) {
    lines.push('', '| Agent | AgentAPI memory | AgentAPI CPU, idle | AgentAPI CPU per turn | Agent memory | Agent CPU per turn |', '| --- | --- | --- | --- | --- | --- |');
    for (const r of result.agents) {
      lines.push(`| ${AGENTS[r.agent].name} | ${range([r.agentapi_mib_idle, ...r.turns.map(t => t.agentapi_mib)], 0, ' MiB')} | ${r.idle_cpu_pct.toFixed(2)}% | ` +
        `${range(r.turns.map(t => t.agentapi_cpu_s), 2, ' s')} | ${range([r.agent_mib_idle, ...r.turns.map(t => t.agent_mib)], 0, ' MiB')} | ` +
        `${range(r.turns.map(t => t.agent_cpu_s), 1, ' s')} |`);
    }
  }
  if (result.long) {
    lines.push('', `A native-demo conversation of ${result.long.prompt_kb} KB prompts restored from \`--state-db\`:`, '',
      '| Tasks | State database | Memory | First `/events?sync=1` replay |', '| --- | --- | --- | --- |');
    for (const r of result.long.checkpoints) {
      lines.push(`| ${r.tasks.toLocaleString('en-US')} | ${r.tasks ? `${r.db_mb.toFixed(r.db_mb < 10 ? 1 : 0)} MB` : ''} | ${r.agentapi_mib.toFixed(0)} MiB | ${size(r.replay_kb)} |`);
    }
  }
  return lines.join('\n');
};

try {
  const cpuinfo = readFileSync('/proc/cpuinfo', 'utf8');
  const result = {
    date: new Date().toISOString().slice(0, 10),
    agentapi_version: version(binary).replace(/^agentapi version /, ''),
    binary,
    binary_mib: statSync(binary).size / 2 ** 20,
    agent_versions: Object.fromEntries(real.map(a => [a, version(which(AGENTS[a].program))])),
    machine: {
      cpu: (cpuinfo.match(/^model name\s*:\s*(.*)$/m)?.[1] ?? os.cpus()[0]?.model ?? 'unknown').replace(/\s+/g, ' '),
      cores: os.cpus().length,
      memory_gib: Math.round(os.totalmem() / 2 ** 30),
      os: `Linux ${os.release()}`,
    },
    turns, idle_seconds: idleSeconds, prompt: PROMPT,
    agents: [],
    long: null,
  };
  for (const agent of agents) result.agents.push(await measureAgent(agent));
  if (checkpoints.length) result.long = await measureLong();
  const json = JSON.stringify(result, null, 1);
  if (options.json) writeFileSync(options.json, json + '\n');
  console.log(json);
  console.log();
  console.log(markdown(result));
} catch (err) {
  failed = true;
  console.error(`measure-resources: ${err.message}`);
} finally {
  await stopAll();
  if (failed || flags.has('keep')) log(`scratch directory kept: ${scratch}`);
  else rmSync(scratch, {recursive: true, force: true});
}
process.exit(failed ? 1 : 0);
