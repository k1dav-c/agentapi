/**
 * Captures the documentation screenshots in docs/screenshots/. The script starts its own AgentAPI
 * servers on a small sample project (the native demo agent, the demo posing as Claude Code for its
 * earlier sessions and subagents, and bash as a PTY agent for TTY mode),
 * walks the chat UI with Chromium, taking screenshots and recording GIFs of the main flows, and stops
 * the servers again. See docs/screenshots/README.md.
 */
import path from 'node:path';
import os from 'node:os';
import net from 'node:net';
import {spawn, execFileSync} from 'node:child_process';
import {mkdtemp, mkdir, writeFile, rm, utimes} from 'node:fs/promises';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {createRequire} from 'node:module';

const moduleName = process.env.AGENTAPI_PLAYWRIGHT_MODULE;
if (!moduleName) {
  console.error('Set AGENTAPI_PLAYWRIGHT_MODULE to the path of playwright-core\'s index.mjs; see docs/screenshots/README.md.');
  process.exit(1);
}
const {chromium} = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
// gifenc and pngjs, installed next to playwright-core, write the GIFs.
const requireBeside = createRequire(path.isAbsolute(moduleName) ? moduleName : fileURLToPath(import.meta.url));
let gifenc, pngjs;
try {
  gifenc = requireBeside('gifenc');
  pngjs = requireBeside('pngjs');
} catch {
  console.error('Install gifenc and pngjs next to playwright-core; see docs/screenshots/README.md.');
  process.exit(1);
}
const repo = fileURLToPath(new URL('..', import.meta.url));
const binary = path.resolve(process.env.AGENTAPI_BINARY || path.join(repo, 'out', 'agentapi'));
const port = Number(process.env.AGENTAPI_SCREENSHOT_PORT || 3311);
const output = path.join(repo, 'docs', 'screenshots');
const images = [];

// The sample project the agent "works on": a static page with a stylesheet, a script, release
// notes with an image, two commits and uncommitted changes for the Diff tab.
const projectFiles = {
  'README.md': `# my-app

A one-page site that the agent works on in the AgentAPI screenshots.

## Run it

Open \`index.html\` in a browser, or serve the folder:

\`\`\`bash
python3 -m http.server 8000
\`\`\`

## Files

- \`index.html\`: the page
- \`styles.css\`: its styles
- \`app.js\`: the theme switch
- \`docs/notes.md\`: release notes
`,
  'index.html': `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>my-app</title>
  <link rel="stylesheet" href="styles.css">
</head>
<body>
  <header>
    <img src="docs/logo.svg" alt="" width="40" height="40">
    <span>my-app</span>
    <button id="theme">Dark theme</button>
  </header>
  <main>
    <h1>Ship the page today</h1>
    <p>A small static site: one HTML file, one stylesheet and a theme switch.</p>
    <section class="cards">
      <article><h2>Fast</h2><p>No build step. Open the file and it works.</p></article>
      <article><h2>Readable</h2><p>Plain HTML and CSS that anyone can edit.</p></article>
      <article><h2>Themed</h2><p>A light and a dark theme, remembered per visitor.</p></article>
    </section>
  </main>
  <script src="app.js"></script>
</body>
</html>
`,
  'styles.css': `:root { --accent: #2563eb; --bg: #f8fafc; --fg: #0f172a; --card: #ffffff; }
body.dark { --bg: #0f172a; --fg: #e2e8f0; --card: #1e293b; }
body { margin: 0; font-family: system-ui, sans-serif; background: var(--bg); color: var(--fg); }
header { display: flex; align-items: center; gap: 12px; padding: 16px 32px; font-weight: 600; }
header button { margin-left: auto; border: 1px solid var(--accent); color: var(--accent); background: none; border-radius: 6px; padding: 6px 12px; }
main { padding: 24px 32px; }
h1 { font-size: 40px; margin: 8px 0; }
.cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; margin-top: 24px; }
.cards article { background: var(--card); border-radius: 10px; padding: 16px; box-shadow: 0 1px 3px rgb(0 0 0 / 0.1); }
.cards h2 { color: var(--accent); font-size: 18px; margin: 0 0 6px; }
`,
  'app.js': `const button = document.getElementById('theme');
const apply = dark => {
  document.body.classList.toggle('dark', dark);
  button.textContent = dark ? 'Light theme' : 'Dark theme';
};
apply(localStorage.getItem('theme') === 'dark');
button.addEventListener('click', () => {
  const dark = !document.body.classList.contains('dark');
  localStorage.setItem('theme', dark ? 'dark' : 'light');
  apply(dark);
});
`,
  'docs/logo.svg': `<svg xmlns="http://www.w3.org/2000/svg" width="48" height="48" viewBox="0 0 40 40"><rect width="40" height="40" rx="9" fill="#2563eb"/><path d="M11 27V13l9 9 9-9v14" fill="none" stroke="#fff" stroke-width="3.5" stroke-linecap="round" stroke-linejoin="round"/></svg>
`,
};
const notes = `# Release notes

![my-app logo](logo.svg)

## 1.1.0

- A **dark theme**, switched from the header and remembered per visitor.
- The cards stack on narrow screens.

| Page | Size | Load time |
| --- | --- | --- |
| \`index.html\` | 1.2 kB | 40 ms |
| \`styles.css\` | 0.9 kB | 12 ms |

\`\`\`js
localStorage.setItem('theme', 'dark');
\`\`\`

See the [README](../README.md) to run the site.
`;

const createProject = async (dir, home) => {
  const write = async (file, text) => {
    await mkdir(path.dirname(path.join(dir, file)), {recursive: true});
    await writeFile(path.join(dir, file), text);
  };
  const git = (args, date) => execFileSync('git', ['-c', 'init.defaultBranch=main', '-c', 'commit.gpgsign=false', ...args], {
    cwd: dir, stdio: 'ignore',
    env: {PATH: process.env.PATH, HOME: home, GIT_AUTHOR_NAME: 'my-app', GIT_AUTHOR_EMAIL: 'dev@example.com',
      GIT_COMMITTER_NAME: 'my-app', GIT_COMMITTER_EMAIL: 'dev@example.com', GIT_AUTHOR_DATE: date, GIT_COMMITTER_DATE: date},
  });
  for (const [file, text] of Object.entries(projectFiles)) await write(file, text);
  git(['init', '-q']);
  git(['add', '-A']);
  git(['commit', '-q', '-m', 'Add the page, its styles and the theme switch'], '2026-01-05T10:00:00Z');
  await write('docs/notes.md', notes);
  git(['add', '-A']);
  git(['commit', '-q', '-m', 'Write the release notes for 1.1.0'], '2026-01-06T09:30:00Z');
  await write('README.md', projectFiles['README.md']
    .replace('A one-page site that the agent works on in the AgentAPI screenshots.', 'A one-page site with a light and a dark theme.')
    + '\n## Deploy\n\nCopy the folder to any static host; there is nothing to build.\n');
  await write('styles.css', projectFiles['styles.css'] + '@media (max-width: 640px) { .cards { grid-template-columns: 1fr; } }\n');
};

// Claude Code's logs for the demo that poses as it: earlier sessions in the directory, and a
// subagent's log next to a session's. Claude Code names a project's directory after its path, with
// every character but letters and digits a dash.
const claudeProject = (home, cwd) => path.join(home, '.claude', 'projects', cwd.replace(/[^a-zA-Z0-9]/g, '-'));
const jsonl = lines => lines.map(line => JSON.stringify(line)).join('\n') + '\n';
// The tokens of a model call, for what the chat says it cost: the longer the conversation, the more
// it reads from the cache.
const claudeUsage = (turn, output = 900) => ({input_tokens: 6, cache_read_input_tokens: 24_000 + turn * 9_000,
  cache_creation_input_tokens: 3_200, output_tokens: output});
const claudeCallLine = (session, at, id, turn, content, output) => ({type: 'assistant', timestamp: at, sessionId: session,
  uuid: `${id}-a`, message: {id: `msg_${id}`, role: 'assistant', model: 'claude-opus-5-5', content,
    stop_reason: 'end_turn', usage: claudeUsage(turn, output)}});
const claudeTurn = (session, at, prompt, reply, id, turn) => [
  {type: 'user', timestamp: at, sessionId: session, uuid: `${id}-u`, message: {role: 'user', content: prompt}},
  claudeCallLine(session, new Date(Date.parse(at) + 20_000).toISOString(), id, turn, [{type: 'text', text: reply}]),
];
const writeClaudeSessions = async (home, cwd) => {
  const dir = claudeProject(home, cwd);
  await mkdir(dir, {recursive: true});
  const sessions = [
    {id: '4f0c2a51-1d6e-4a3b-9c1e-7a2b5d8e9f10', title: 'Dark theme for my-app', at: '2026-10-19T15:20:00Z', turns: [
      ['Add a dark theme to `my-app/styles.css`, switched from the header.', 'Added a dark theme: the colors are variables, and `body.dark` swaps them.'],
      ['Remember the choice per visitor.', 'The switch now stores the theme in localStorage and applies it on load.'],
      ['Check the contrast of the cards in the dark theme.', 'The card text passes WCAG AA in both themes.'],
    ]},
    {id: '9b3e7d24-6a81-4c5f-8e2d-1f4a6b9c0d37', title: 'Release notes for 1.1.0', at: '2026-10-18T10:05:00Z', turns: [
      ['Write the release notes for 1.1.0 in `my-app/docs/notes.md`.', 'Wrote the notes: the dark theme, the stacked cards, and a size table.'],
      ['Add the logo at the top.', 'The notes start with the logo now.'],
    ]},
    {id: 'c27a9e13-5b40-4d8f-a6c1-3e9f2b7d5a84', title: '', at: '2026-10-16T08:40:00Z', turns: [
      ['Why do the cards overflow on a phone?', 'The grid has three fixed columns; below 640px they now stack.'],
    ]},
  ];
  for (const s of sessions) {
    const lines = s.turns.flatMap(([prompt, reply], i) => claudeTurn(s.id, new Date(Date.parse(s.at) + i * 300_000).toISOString(), prompt, reply, `${s.id.slice(0, 8)}${i}`, i));
    if (s.title) lines.push({type: 'ai-title', aiTitle: s.title, sessionId: s.id});
    const file = path.join(dir, `${s.id}.jsonl`);
    await writeFile(file, jsonl(lines));
    const at = new Date(Date.parse(s.at) + s.turns.length * 300_000);
    await utimes(file, at, at);
  }
};
// The log of the session the demo posing as Claude Code has open, which the demo doesn't write: a
// model call for each of its replies, for what the task cost.
const writeClaudeCurrent = async (home, cwd, session, prompt, rich) => {
  const lines = [{type: 'user', timestamp: prompt.time, sessionId: session, uuid: 'cur-u', message: {role: 'user', content: prompt.content}}];
  rich.filter(m => m.role === 'assistant').forEach((m, i) =>
    lines.push(claudeCallLine(session, m.timestamp, `cur${i}`, i, [{type: 'text', text: '…'}], 400 + i * 150)));
  await writeFile(path.join(claudeProject(home, cwd), `${session}.jsonl`), jsonl(lines));
};
// The log of a subagent the demo's Agent tool call started, as Claude Code writes it.
const writeClaudeSubagent = async (home, cwd, session, toolUseId, startedAt) => {
  const dir = path.join(claudeProject(home, cwd), session, 'subagents');
  await mkdir(dir, {recursive: true});
  const at = seconds => new Date(Date.parse(startedAt) + seconds * 1000).toISOString();
  const base = {isSidechain: true, agentId: 'a7d2c91e', sessionId: session};
  const read = 'toolu_sub_read';
  const grep = 'toolu_sub_grep';
  await writeFile(path.join(dir, 'agent-a7d2c91e.jsonl'), jsonl([
    {...base, type: 'user', timestamp: at(0), uuid: 's0', message: {role: 'user', content: 'Read docs/notes.md and list what the 1.1.0 notes are missing.'}},
    {...base, type: 'assistant', timestamp: at(2), uuid: 's1', message: {id: 'msg_sub1', role: 'assistant', model: 'claude-sonnet-5-5', usage: claudeUsage(0, 300), content: [
      {type: 'thinking', thinking: '**Reading the notes**\n\nRelease notes usually give a date and where to get the release; I will check for both.'},
      {type: 'tool_use', id: read, name: 'Read', input: {file_path: path.join(cwd, 'my-app', 'docs', 'notes.md')}}], stop_reason: 'tool_use'}},
    {...base, type: 'user', timestamp: at(3), uuid: 's2', message: {role: 'user', content: [{type: 'tool_result', tool_use_id: read, content: notes}]}},
    {...base, type: 'assistant', timestamp: at(5), uuid: 's3', message: {id: 'msg_sub2', role: 'assistant', model: 'claude-sonnet-5-5', usage: claudeUsage(1, 120), content: [
      {type: 'tool_use', id: grep, name: 'Bash', input: {command: 'git -C my-app tag --list', description: 'List the release tags'}}], stop_reason: 'tool_use'}},
    {...base, type: 'user', timestamp: at(6), uuid: 's4', message: {role: 'user', content: [{type: 'tool_result', tool_use_id: grep, content: ''}]}},
    {...base, type: 'assistant', timestamp: at(8), uuid: 's5', message: {id: 'msg_sub3', role: 'assistant', model: 'claude-sonnet-5-5', usage: claudeUsage(2, 600), content: [
      {type: 'text', text: 'The 1.1.0 notes miss two things:\n\n- **The release date.** Nothing says when 1.1.0 shipped, and there is no `v1.1.0` tag yet.\n- **Where to get it.** There is no download link or a line on how to update.'}], stop_reason: 'end_turn'}},
  ]));
  await writeFile(path.join(dir, 'agent-a7d2c91e.meta.json'), JSON.stringify({agentType: 'general-purpose', description: 'Check the release notes', toolUseId}));
};

const portFree = p => new Promise(resolve => {
  const socket = net.connect({host: '127.0.0.1', port: p});
  socket.once('connect', () => { socket.destroy(); resolve(false); });
  socket.once('error', () => resolve(true));
});

const servers = [];
const startServer = (name, cwd, env, args) => {
  const child = spawn(binary, ['server', '--bind', '127.0.0.1', '--allowed-hosts', 'localhost,127.0.0.1', ...args], {
    cwd, env, stdio: ['ignore', 'ignore', 'pipe'],
  });
  let log = '';
  child.stderr.on('data', chunk => { log = (log + chunk).slice(-4000); });
  const exited = new Promise(resolve => child.once('exit', resolve));
  servers.push({name, child, exited, log: () => log});
};

const poll = async (check, label, ms = 40_000) => {
  const deadline = Date.now() + ms;
  do {
    if (await check().catch(() => false)) return;
    await new Promise(resolve => setTimeout(resolve, 150));
  } while (Date.now() < deadline);
  throw new Error(`Timed out waiting for ${label}`);
};
const client = base => {
  const request = async (route, method = 'GET', body) => {
    const response = await fetch(`${base}${route}`, {method,
      headers: body === undefined ? undefined : {'Content-Type': 'application/json'},
      body: body === undefined ? undefined : JSON.stringify(body)});
    if (!response.ok) throw new Error(`${method} ${route}: ${response.status}`);
    const text = await response.text();
    return text ? JSON.parse(text) : undefined;
  };
  const status = () => request('/status');
  const idle = () => poll(async () => {
    const state = await status();
    return state.status === 'stable' && state.lifecycle === 'ready' && !state.pending_requests;
  }, 'the agent to finish');
  const send = async content => {
    const before = (await status()).turn;
    await request('/message', 'POST', {content, type: 'user'});
    await poll(async () => (await status()).turn > before, 'a new turn');
  };
  const pending = kind => poll(async () => (await request('/requests')).requests.some(r => r.kind === kind && r.status === 'pending'), `a ${kind} request`);
  return {base, request, status, idle, send, pending};
};

// Screenshots hide the Next.js dev overlay and toasts, which only cover parts of the views.
const screenshotStyle = 'nextjs-portal, [data-sonner-toaster] { visibility: hidden !important; }';
const save = async (target, name, options = {}) => {
  await target.screenshot({path: path.join(output, name), animations: 'disabled', style: screenshotStyle, ...options});
  images.push(name);
  console.log(`Captured ${name}`);
};
const settle = async page => { await page.mouse.move(0, 0); await page.waitForTimeout(300); };
const shot = async (page, name) => { await settle(page); await save(page, name); };
const elementShot = async (page, locator, name) => { await settle(page); await save(locator, name); };
// A region around some elements, for menus and cards that would be lost in a whole window.
const regionShot = async (page, name, locators, pad = 16) => {
  await settle(page);
  const boxes = await Promise.all(locators.map(locator => locator.boundingBox()));
  const viewport = page.viewportSize();
  const x = Math.max(0, Math.min(...boxes.map(b => b.x)) - pad);
  const y = Math.max(0, Math.min(...boxes.map(b => b.y)) - pad);
  const right = Math.min(viewport.width, Math.max(...boxes.map(b => b.x + b.width)) + pad);
  let bottom = Math.min(viewport.height, Math.max(...boxes.map(b => b.y + b.height)) + pad);
  // Keep the line of keys under the composer whole instead of cutting through it.
  if (viewport.height - bottom < 48) bottom = viewport.height;
  await save(page, name, {clip: {x, y, width: right - x, height: bottom - y}});
};
// The top of a tall dialog, whose lower part would be empty.
const topShot = async (page, locator, name, height) => {
  await settle(page);
  const box = await locator.boundingBox();
  await save(page, name, {clip: {x: box.x, y: box.y, width: box.width, height: Math.min(box.height, height)}});
};

// GIFs. Headless Chromium draws no mouse pointer, so the pages recorded get one that follows the
// mouse and shows clicks.
const pointerScript = () => {
  const install = () => {
    if (document.getElementById('docs-pointer')) return;
    const style = document.createElement('style');
    style.textContent = `#docs-pointer{position:fixed;left:0;top:0;width:22px;height:22px;margin:-11px 0 0 -11px;border-radius:50%;
      background:rgba(37,99,235,.25);border:2px solid rgba(37,99,235,.9);pointer-events:none;z-index:2147483647;
      transition:transform .12s ease-out,background .12s}#docs-pointer.down{transform:scale(.7);background:rgba(37,99,235,.55)}`;
    document.head.appendChild(style);
    const dot = document.createElement('div');
    dot.id = 'docs-pointer';
    dot.style.display = 'none';
    document.body.appendChild(dot);
    addEventListener('mousemove', (event) => { dot.style.display = 'block'; dot.style.left = `${event.clientX}px`; dot.style.top = `${event.clientY}px`; }, true);
    addEventListener('mousedown', () => dot.classList.add('down'), true);
    addEventListener('mouseup', () => dot.classList.remove('down'), true);
  };
  if (document.readyState === 'loading') addEventListener('DOMContentLoaded', install);
  else install();
};
// Moves the pointer to the middle of locator in steps, so a GIF shows where it goes, and clicks.
const tap = async (page, locator) => {
  // A key far along the terminal's key bar is scrolled to first, as on a phone.
  await locator.scrollIntoViewIfNeeded();
  const box = await locator.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2, {steps: 14});
  await page.waitForTimeout(160);
  await page.mouse.down();
  await page.waitForTimeout(90);
  await page.mouse.up();
  await page.waitForTimeout(160);
};
const typeSlowly = (page, text) => page.keyboard.type(text, {delay: 28});

// writeGif encodes frames (PNG screenshots taken at a time in ms) as an endless GIF: frames that
// show nothing new are dropped, all frames share one palette, and each frame after the first keeps
// only the pixels that changed (the rest is transparent over the frame before).
const writeGif = async (file, frames, hold) => {
  const {GIFEncoder, quantize, applyPalette} = gifenc;
  const decoded = frames.map((frame) => ({at: frame.at, ...pngjs.PNG.sync.read(frame.png)}));
  const shown = decoded.filter((frame, i) => i === 0 || Buffer.compare(frame.data, decoded[i - 1].data) !== 0);
  const {width, height} = shown[0];
  const step = Math.max(1, Math.floor(shown.length / 12));
  const picked = shown.filter((_, i) => i % step === 0);
  const sample = new Uint8Array(picked.length * Math.ceil(width * height / 4) * 4);
  let n = 0;
  for (const frame of picked) {
    for (let p = 0; p < frame.data.length; p += 16) {
      sample.set(frame.data.subarray(p, p + 4), n);
      n += 4;
    }
  }
  const palette = quantize(sample.subarray(0, n), 255);
  const transparentIndex = palette.length;
  const gif = GIFEncoder();
  shown.forEach((frame, k) => {
    const index = applyPalette(frame.data, palette);
    if (k > 0) {
      const before = shown[k - 1].data;
      for (let i = 0, p = 0; i < index.length; i += 1, p += 4) {
        if (frame.data[p] === before[p] && frame.data[p + 1] === before[p + 1] && frame.data[p + 2] === before[p + 2]) index[i] = transparentIndex;
      }
    }
    const next = shown[k + 1];
    gif.writeFrame(index, width, height, {
      palette: k === 0 ? [...palette, [255, 0, 255]] : undefined,
      delay: next ? Math.max(20, next.at - frame.at) : hold,
      transparent: k > 0,
      transparentIndex,
      dispose: 1,
    });
  });
  gif.finish();
  await writeFile(file, gif.bytes());
  return shown.length;
};
// recordGif records what action does on page as name, a frame about every interval ms.
const recordGif = async (page, name, action, {interval = 110, hold = 2000} = {}) => {
  const frames = [];
  let recording = true;
  const start = Date.now();
  const loop = (async () => {
    while (recording) {
      const taken = Date.now();
      frames.push({at: taken - start, png: await page.screenshot({style: screenshotStyle})});
      const rest = interval - (Date.now() - taken);
      if (rest > 0) await new Promise((resolve) => setTimeout(resolve, rest));
    }
  })();
  try {
    await page.waitForTimeout(500);
    await action();
    await page.waitForTimeout(700);
  } finally {
    recording = false;
    await loop;
  }
  const count = await writeGif(path.join(output, name), frames, hold);
  images.push(name);
  console.log(`Recorded ${name} (${count} frames)`);
};

const contextOptions = {deviceScaleFactor: 1, colorScheme: 'light', locale: 'en-US', timezoneId: 'UTC', reducedMotion: 'reduce'};
const errors = [];
const openPage = async (browser, api, options = {}) => {
  const context = await browser.newContext({...contextOptions, viewport: {width: 1440, height: 1000}, ...options});
  context.on('weberror', event => errors.push(event.error().message));
  await context.addInitScript(() => { try { localStorage.setItem('theme', 'light'); } catch {} });
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  const url = new URL(`${api}/chat/`);
  url.searchParams.set('url', api);
  const navigation = await page.goto(url.href);
  if (navigation && !navigation.ok()) throw new Error(`The chat UI returned HTTP ${navigation.status()}`);
  await page.getByRole('textbox', {name: 'Task message'}).waitFor();
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(800);
  return page;
};

const root = await mkdtemp(path.join(os.tmpdir(), 'agentapi-docs-'));
const home = path.join(root, 'home');
const project = path.join(root, 'my-app');
let browser;
try {
  for (const p of [port, port + 1, port + 2]) {
    if (!await portFree(p)) throw new Error(`Port ${p} is in use; set AGENTAPI_SCREENSHOT_PORT to the first of three free ports.`);
  }
  // A clean environment: no tokens or workspace links of the shell the script runs in, and a
  // neutral prompt in the terminal.
  await mkdir(home, {recursive: true});
  await writeFile(path.join(home, '.bash_profile'), "PS1='my-app $ '\nexport PS1\n");
  await writeFile(path.join(home, '.hushlogin'), '');
  await createProject(project, home);
  const env = {HOME: home, PATH: '/usr/local/bin:/usr/bin:/bin', SHELL: '/bin/bash', LANG: 'C.UTF-8', TERM: 'xterm-256color'};
  startServer('native demo', project, env, ['--port', String(port), '--transport', 'native', '--type', 'custom', '--', 'native-demo']);
  startServer('PTY bash', project, {...env, PS1: 'my-app $ '}, ['--port', String(port + 1), '--type', 'custom', '--term-width', '100', '--', 'bash', '--norc', '--noprofile']);
  // The demo posing as Claude Code, one directory above the project: its earlier sessions and
  // subagents come from the Claude Code logs written into the script's home.
  await writeClaudeSessions(home, root);
  startServer('demo as Claude Code', root, env, ['--port', String(port + 2), '--transport', 'native', '--type', 'claude', '--', 'native-demo']);
  const demo = client(`http://127.0.0.1:${port}`);
  const pty = client(`http://127.0.0.1:${port + 1}`);
  const posing = client(`http://127.0.0.1:${port + 2}`);
  await poll(async () => (await demo.status()).lifecycle === 'ready', 'the native demo server');
  await poll(async () => !!(await pty.status()), 'the PTY server');
  await poll(async () => (await posing.status()).lifecycle === 'ready', 'the demo posing as Claude Code');
  const {agentapi_version: version} = await demo.status();

  browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || undefined,
    args: ['--no-sandbox', '--disable-dev-shm-usage']});

  // Two finished tasks: the transcript, its menus, the composer's menus and the Explorer.
  await demo.idle();
  await demo.send('Review `README.md` and the page in `index.html`.');
  await demo.idle();
  // The link comes after the part of the message the demo repeats in its reply, which it cuts at 60
  // characters.
  await demo.send('List what is missing from `docs/notes.md` compared with the latest release notes at https://github.com/coder/agentapi/releases.');
  await demo.idle();
  const page = await openPage(browser, demo.base);
  await page.getByText('the latest release notes', {exact: false}).last().waitFor();
  await shot(page, 'chat-desktop.png');

  const toolCall = page.locator('details').filter({has: page.locator('summary').filter({hasText: /^\$?\s*ls/})}).last();
  await toolCall.locator('summary').first().click();
  await elementShot(page, toolCall, 'tool-call.png');
  await toolCall.locator('summary').first().click();

  await page.getByRole('button', {name: 'Task 2 actions'}).click();
  const exportItem = page.getByRole('menuitem', {name: 'Export Markdown'});
  await exportItem.waitFor();
  await regionShot(page, 'task-menu.png', [page.locator('button[title="Task 2 actions"]'), page.getByRole('menu')], 24);
  await page.getByRole('menuitem', {name: 'Preview Markdown'}).click();
  await page.getByRole('dialog').waitFor();
  await elementShot(page, page.getByRole('dialog'), 'task-preview.png');
  await page.keyboard.press('Escape');

  await page.getByRole('button', {name: /Open session details/}).click();
  await page.getByRole('menuitem', {name: 'Keyboard shortcuts'}).waitFor();
  await regionShot(page, 'session-details.png', [page.getByRole('menu'), page.locator('button[aria-label$="Open session details"]')]);
  await page.getByRole('menuitem', {name: 'Keyboard shortcuts'}).click();
  await page.getByRole('dialog').waitFor();
  await elementShot(page, page.getByRole('dialog'), 'keyboard-shortcuts.png');
  await page.keyboard.press('Escape');

  // Menus hide the rest of the page from the accessibility tree, so the composer is found by CSS.
  const composer = page.locator('form:has(textarea[aria-label="Task message"])');
  await page.locator('button[title^="Permission mode"]').click();
  await page.getByRole('menuitemradio', {name: /^Plan/}).waitFor();
  await regionShot(page, 'permission-mode.png', [page.getByRole('menu'), composer]);
  await page.keyboard.press('Escape');
  await page.locator('button[title="Model and reasoning effort"]').click();
  await page.getByRole('menuitemradio', {name: /Swift/}).waitFor();
  await regionShot(page, 'model-settings.png', [page.getByRole('menu'), composer]);
  await page.keyboard.press('Escape');
  await page.locator('button[title^="Context"]').click();
  const usage = page.getByText('Context window', {exact: true}).locator('xpath=ancestor::*[@data-radix-popper-content-wrapper][1]');
  await usage.waitFor();
  await regionShot(page, 'context-usage.png', [usage, composer]);
  await page.keyboard.press('Escape');

  const box = page.getByRole('textbox', {name: 'Task message'});
  await box.fill('/');
  const commands = page.getByLabel('Chat commands');
  await commands.waitFor();
  await regionShot(page, 'slash-commands.png', [commands, composer]);
  await box.fill('');

  const mockup = path.join(root, 'mockup.png');
  const render = await browser.newPage({viewport: {width: 900, height: 560}});
  await render.goto(pathToFileURL(path.join(project, 'index.html')).href);
  await render.screenshot({path: mockup});
  await render.close();
  await page.locator('input[type=file][aria-label="Attach files"]').setInputFiles(mockup);
  await box.fill('Make the cards on the page match this mockup.');
  await page.getByText('Attached', {exact: true}).waitFor();
  await regionShot(page, 'attachments.png', [composer]);
  await page.getByRole('button', {name: 'Remove attachment'}).click();
  await box.fill('');

  await page.getByRole('button', {name: 'Open explorer'}).click();
  const explorer = page.getByRole('dialog');
  await page.getByRole('tab', {name: 'Index', exact: true}).click();
  await page.waitForTimeout(400);
  await topShot(page, explorer, 'explorer-index.png', 440);
  await page.getByRole('tab', {name: 'Links', exact: true}).click();
  await explorer.getByRole('link', {name: 'https://github.com/coder/agentapi/releases'}).waitFor();
  await topShot(page, explorer, 'explorer-links.png', 440);
  await page.getByRole('tab', {name: 'Session', exact: true}).click();
  await page.getByRole('button', {name: 'Restart agent'}).click();
  await page.getByRole('button', {name: 'Click again to restart'}).waitFor();
  await topShot(page, explorer, 'explorer-session.png', 440);
  // The webhook, filled in but not saved, so the demo sends nothing.
  await page.getByRole('tab', {name: 'Webhook', exact: true}).click();
  await explorer.locator('[data-webhook-settings]').waitFor();
  await explorer.getByPlaceholder('https://dashboard.example.com/agentapi/events').fill('https://dashboard.example.com/agentapi/events');
  for (const group of ['tasks', 'needs-you', 'instance']) await explorer.locator(`[data-webhook-group="${group}"] input`).check();
  await explorer.locator('[data-webhook-group="status"] input').uncheck();
  await explorer.locator('input[type=password]').fill('example-secret');
  await explorer.getByPlaceholder('For the receiver to link to this chat').fill('https://agent.example.com');
  // The default is this machine's name.
  await explorer.getByLabel('Instance ID').fill('my-app-dev:3284');
  await topShot(page, explorer, 'explorer-webhook.png', 1000);
  await page.keyboard.press('Escape');

  const viewer = async (file, tab, name) => {
    await page.getByRole('button', {name: `Open file: ${file}`, exact: true}).first().click();
    await page.getByRole('tab', {name: 'File', exact: true}).waitFor();
    await page.getByRole('tab', {name: tab, exact: true}).click();
    await page.waitForTimeout(1500);
    await elementShot(page, page.getByRole('dialog').last(), name);
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  };
  await viewer('index.html', 'File', 'file-viewer.png');
  await viewer('index.html', 'Preview', 'file-preview.png');
  await viewer('docs/notes.md', 'Preview', 'file-markdown.png');
  await viewer('README.md', 'Diff', 'file-diff.png');

  // Searching opens the tool calls it matches, so it comes after the views with the transcript behind
  // a menu.
  await page.getByRole('button', {name: 'Search tasks'}).click();
  await page.keyboard.type('notes');
  await page.waitForTimeout(600);
  await shot(page, 'search.png');
  await page.getByRole('button', {name: 'Close search'}).click();

  await page.getByRole('button', {name: 'Open the terminal'}).click();
  const terminalRows = page.locator('.xterm-rows');
  await terminalRows.waitFor();
  await poll(async () => (await terminalRows.innerText()).includes('my-app $'), 'the shell prompt');
  await page.keyboard.type('git status --short\n');
  await page.waitForTimeout(500);
  await page.keyboard.type('git log --oneline\n');
  await poll(async () => (await terminalRows.innerText()).includes('release notes'), 'the git log');
  await page.waitForTimeout(500);
  await shot(page, 'terminal.png');
  await page.getByRole('button', {name: 'Back to chat'}).click();

  const phoneOptions = {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true};
  const phone = await openPage(browser, demo.base, phoneOptions);
  await phone.getByText('the latest release notes', {exact: false}).last().waitFor();
  await shot(phone, 'chat-mobile.png');
  await phone.context().close();

  // Requests: an approval, questions and a plan, each answered by interrupting the turn.
  await demo.send('Rebuild the release artifacts; I will approve the command.');
  await demo.pending('approval');
  await page.locator('[data-request-kind="approval"]').waitFor();
  await shot(page, 'native-approval.png');
  await demo.request('/interrupt', 'POST');
  await demo.idle();
  await demo.send('Ask me which checks to run before the release.');
  await demo.pending('question');
  await page.locator('[data-request-kind="question"]').waitFor();
  await shot(page, 'native-question.png');
  const phoneQuestion = await openPage(browser, demo.base, phoneOptions);
  await phoneQuestion.locator('[data-request-kind="question"]').waitFor();
  await shot(phoneQuestion, 'mobile-question.png');
  await phoneQuestion.context().close();
  await demo.request('/interrupt', 'POST');
  await demo.idle();
  await demo.send('Plan the dark theme before changing anything.');
  await demo.pending('plan');
  await page.locator('[data-request-kind="plan"]').waitFor();
  await shot(page, 'native-plan.png');
  await demo.request('/interrupt', 'POST');
  await demo.idle();

  // A long turn with tasks waiting in the queue and the send menu open.
  await demo.send('Take the long route: refactor the layout step by step.');
  await page.getByText('still working', {exact: false}).first().waitFor();
  await box.fill('Then add a dark theme to `styles.css`.');
  await box.press('Enter');
  await page.waitForTimeout(400);
  await box.fill('Finally update the screenshots in `README.md`.');
  await box.press('Enter');
  await page.waitForTimeout(400);
  await box.fill('Keep the header sticky.');
  await page.getByRole('button', {name: 'More ways to send'}).click();
  await page.getByRole('menuitem', {name: /Interrupt & send/}).waitFor();
  await shot(page, 'queue-steer.png');
  await page.keyboard.press('Escape');
  await box.fill('');
  await demo.request('/messages', 'DELETE');
  await demo.idle();

  // A turn a usage limit stopped, with a task held in the queue until the limit resets. The demo
  // reaches the limit after the approval it asks for, while the task waits in the queue.
  await demo.send('Review `README.md` and the page in `index.html`.');
  await demo.idle();
  await demo.send('Rebuild the release artifacts; I will approve the command. Stop if you hit the limit.');
  await demo.pending('approval');
  await box.fill('Then update the screenshots in `README.md`.');
  await box.press('Enter');
  await page.getByText('Queued tasks', {exact: false}).waitFor();
  const approval = (await demo.request('/requests')).requests.find(r => r.status === 'pending');
  await demo.request(`/requests/${approval.id}/respond`, 'POST', {option_id: approval.options.find(o => o.kind === 'allow').id});
  await page.locator('[data-limit-stop]').waitFor();
  await page.getByText('held until the usage limit resets', {exact: false}).waitFor();
  await shot(page, 'limit-card.png');
  await demo.request('/messages', 'DELETE');
  await demo.idle();

  // An expired prompt cache over a large context, while the agent is idle.
  await demo.send('Review `README.md` and the page in `index.html`.');
  await demo.idle();
  await demo.send('Summarize the work so far; the cache has gone cold.');
  await demo.idle();
  await page.locator('[data-cold-cache]').waitFor();
  await regionShot(page, 'cold-cache.png', [page.locator('[data-cold-cache]'), composer]);
  await demo.request('/messages', 'DELETE');
  await demo.idle();

  // GIFs of the main flows, on a smaller window with a visible pointer.
  const openGifPage = async (options = {viewport: {width: 1100, height: 720}}) => {
    const context = await browser.newContext({...contextOptions, ...options});
    context.on('weberror', event => errors.push(event.error().message));
    await context.addInitScript(() => { try { localStorage.setItem('theme', 'light'); } catch {} });
    await context.addInitScript(pointerScript);
    const gifPage = await context.newPage();
    gifPage.setDefaultTimeout(20_000);
    const url = new URL(`${demo.base}/chat/`);
    url.searchParams.set('url', demo.base);
    await gifPage.goto(url.href);
    await gifPage.getByRole('textbox', {name: 'Task message'}).waitFor();
    await gifPage.evaluate(() => document.fonts.ready);
    await gifPage.waitForTimeout(600);
    return gifPage;
  };
  await demo.request('/messages', 'DELETE');
  await demo.idle();
  const gif = await openGifPage();
  const gifBox = gif.getByRole('textbox', {name: 'Task message'});
  const sendTask = async (text) => {
    await tap(gif, gifBox);
    await typeSlowly(gif, text);
    await gif.waitForTimeout(250);
    await gif.keyboard.press('Enter');
  };

  // A task: the agent thinks, runs a tool and replies.
  await recordGif(gif, 'demo-task.gif', async () => {
    await sendTask('Review `README.md` and the page in `index.html`.');
    await demo.idle();
    await gif.getByText('Here is what I did for', {exact: false}).last().waitFor();
  });

  // An approval: the command waits for Allow.
  await recordGif(gif, 'demo-approval.gif', async () => {
    await sendTask('Rebuild the release artifacts; I will approve the command.');
    const card = gif.locator('[data-request-kind="approval"]');
    await card.waitFor();
    await gif.waitForTimeout(900);
    await tap(gif, card.getByRole('button', {name: /^1?\s*Allow$/}));
    await demo.idle();
  });

  // Questions: one step at a time, sent together.
  await recordGif(gif, 'demo-question.gif', async () => {
    await sendTask('Ask me which checks to run before the release.');
    const card = gif.locator('[data-request-kind="question"]');
    await card.waitFor();
    await gif.waitForTimeout(700);
    await tap(gif, card.getByRole('radio').first());
    await tap(gif, card.getByRole('checkbox').nth(0));
    await tap(gif, card.getByRole('checkbox').nth(2));
    await tap(gif, card.getByRole('button', {name: 'Next'}));
    await card.locator('textarea, input[type=text]').first().waitFor();
    await tap(gif, card.locator('textarea, input[type=text]').first());
    await typeSlowly(gif, 'Run the docs build too.');
    await tap(gif, card.getByRole('button').last());
    await demo.idle();
  });

  // The queue and steering while a long turn works, then Stop.
  await recordGif(gif, 'demo-queue.gif', async () => {
    await sendTask('Take the long route: refactor the layout step by step.');
    await gif.getByText('still working', {exact: false}).first().waitFor();
    // Sending moves the focus away from the message box.
    await sendTask('Then add a dark theme to `styles.css`.');
    await gif.waitForTimeout(700);
    await tap(gif, gifBox);
    await typeSlowly(gif, 'Keep the header sticky.');
    await tap(gif, gif.getByRole('button', {name: 'More ways to send'}));
    await tap(gif, gif.getByRole('menuitem', {name: /^Steer/}));
    await gif.waitForTimeout(2500);
    const stop = gif.getByRole('button', {name: 'Stop agent'});
    if (await stop.isVisible()) await tap(gif, stop);
    await gif.waitForTimeout(1500);
  });
  await demo.request('/messages', 'DELETE');
  await demo.idle();
  await demo.send('Review `README.md` and the page in `index.html`.');
  await demo.idle();
  await gif.reload();
  await gifBox.waitFor();
  await gif.getByText('Here is what I did for', {exact: false}).last().waitFor();

  // Settings: the permission mode, the model and effort, and the context.
  await recordGif(gif, 'demo-settings.gif', async () => {
    await tap(gif, gif.locator('button[title^="Permission mode"]'));
    await gif.waitForTimeout(500);
    await tap(gif, gif.getByRole('menuitemradio', {name: /^Ask/}));
    await gif.waitForTimeout(500);
    await tap(gif, gif.locator('button[title="Model and reasoning effort"]'));
    await gif.waitForTimeout(500);
    await tap(gif, gif.getByRole('menuitemradio', {name: /^Deep/}));
    await gif.waitForTimeout(500);
    await tap(gif, gif.locator('button[title="Model and reasoning effort"]'));
    await gif.waitForTimeout(500);
    await tap(gif, gif.getByRole('menuitemradio', {name: /^max/}));
    await gif.waitForTimeout(500);
    await tap(gif, gif.locator('button[title^="Context"]'));
    await gif.waitForTimeout(1600);
    await gif.keyboard.press('Escape');
  });
  // Back to the defaults for the views after.
  await demo.request('/settings', 'PUT', {permission_mode: 'bypass', model: 'demo-swift', effort: 'medium'});

  // Files: the file, its preview, and a diff.
  await recordGif(gif, 'demo-files.gif', async () => {
    await tap(gif, gif.getByRole('button', {name: 'Open file: index.html', exact: true}).first());
    await gif.getByRole('tab', {name: 'Preview', exact: true}).waitFor();
    await gif.waitForTimeout(900);
    await tap(gif, gif.getByRole('tab', {name: 'Preview', exact: true}));
    await gif.waitForTimeout(1400);
    await gif.keyboard.press('Escape');
    await gif.waitForTimeout(400);
    await tap(gif, gif.getByRole('button', {name: 'Open file: README.md', exact: true}).first());
    await gif.getByRole('tab', {name: 'Diff', exact: true}).waitFor();
    await tap(gif, gif.getByRole('tab', {name: 'Diff', exact: true}));
    await gif.waitForTimeout(1600);
    await gif.keyboard.press('Escape');
  });

  // The shell terminal: commands, a link to open, and a held Ctrl.
  await recordGif(gif, 'demo-terminal.gif', async () => {
    await tap(gif, gif.getByRole('button', {name: 'Open the terminal'}));
    const rows = gif.locator('.xterm-rows');
    await rows.waitFor();
    await poll(async () => (await rows.innerText()).includes('my-app $'), 'the shell prompt');
    await typeSlowly(gif, 'git status --short\n');
    await gif.waitForTimeout(500);
    await typeSlowly(gif, 'echo https://auth.openai.com/codex/device\n');
    await gif.locator('[data-terminal-link]').waitFor();
    await gif.waitForTimeout(900);
    await tap(gif, gif.getByRole('button', {name: 'Ctrl', exact: true}));
    await gif.keyboard.type('l');
    await gif.waitForTimeout(900);
    await tap(gif, gif.getByRole('button', {name: 'Back to chat'}));
  });
  await gif.context().close();

  // A phone: the terminal's key bar and its link.
  const phoneGif = await openGifPage(phoneOptions);
  await recordGif(phoneGif, 'demo-phone.gif', async () => {
    await tap(phoneGif, phoneGif.getByRole('button', {name: 'Open the terminal'}));
    const rows = phoneGif.locator('.xterm-rows');
    await rows.waitFor();
    await poll(async () => (await rows.innerText()).includes('my-app $'), 'the shell prompt');
    // On a touch screen the keyboard, and with it typing, starts from the Keyboard button.
    await tap(phoneGif, phoneGif.getByRole('button', {name: 'Show keyboard'}));
    await typeSlowly(phoneGif, 'ls docs');
    await tap(phoneGif, phoneGif.getByRole('button', {name: 'Send Enter'}));
    await phoneGif.waitForTimeout(500);
    await tap(phoneGif, phoneGif.getByRole('button', {name: 'Send Arrow up'}));
    await phoneGif.waitForTimeout(500);
    await tap(phoneGif, phoneGif.getByRole('button', {name: 'Send Ctrl+U'}));
    await typeSlowly(phoneGif, 'echo https://auth.openai.com/codex/device');
    await tap(phoneGif, phoneGif.getByRole('button', {name: 'Send Enter'}));
    await phoneGif.locator('[data-terminal-link]').waitFor();
    await phoneGif.waitForTimeout(1200);
  }, {interval: 130});
  await phoneGif.context().close();
  await demo.request('/messages', 'DELETE');
  await demo.idle();

  // The demo posing as Claude Code: its earlier sessions, a subagent, and a file found below its
  // working directory.
  await posing.idle();
  await posing.send('Delegate checking the release notes in `docs/notes.md` to a subagent.');
  await posing.idle();
  const {current: session} = await posing.request('/sessions');
  const {messages: rich} = await posing.request('/rich-messages');
  const agentCall = rich.flatMap(m => m.content).find(b => b.type === 'tool_use' && b.tool_name === 'Agent');
  await writeClaudeSubagent(home, root, session, agentCall.tool_use_id, rich.find(m => m.content.includes(agentCall))?.timestamp ?? new Date().toISOString());
  const {messages: plain} = await posing.request('/messages');
  await writeClaudeCurrent(home, root, session, plain.findLast(m => m.role === 'user'), rich);
  const claude = await openPage(browser, posing.base);
  await claude.getByText('Here is what I did for', {exact: false}).last().waitFor();
  // What the task cost, beside its start and duration.
  await claude.locator('[data-task-cost]').first().waitFor();
  await regionShot(claude, 'task-cost.png', [claude.locator('section[id^="task-"] header').last(), claude.locator('[data-task-cost]').last()], 20);
  await claude.getByRole('button', {name: 'Open explorer'}).click();
  await claude.getByRole('tab', {name: 'Session', exact: true}).click();
  await claude.locator('[data-session]').first().waitFor();
  await claude.locator('[data-spend-card]').waitFor();
  await claude.locator('[data-session]').nth(1).getByRole('button', {name: 'Open'}).click();
  await claude.getByRole('button', {name: 'Click again to open'}).waitFor();
  await topShot(claude, claude.getByRole('dialog'), 'explorer-sessions.png', 700);
  await claude.keyboard.press('Escape');
  await claude.locator('[data-open-subagent]').first().click();
  await claude.locator('[data-lineage] details').first().waitFor();
  await claude.waitForTimeout(600);
  await topShot(claude, claude.getByRole('dialog'), 'lineage.png', 520);
  await claude.keyboard.press('Escape');
  // docs/notes.md is my-app's, one directory below where this agent works.
  await claude.getByRole('button', {name: 'Open file: docs/notes.md', exact: true}).first().click();
  await claude.getByText('this is the file found for it', {exact: false}).waitFor();
  await claude.waitForTimeout(1200);
  await topShot(claude, claude.getByRole('dialog').last(), 'file-find.png', 520);
  await claude.keyboard.press('Escape');
  await claude.context().close();

  // A lost login. The card names the host and user to log in as; the screenshot shows placeholders.
  await demo.send('Review `README.md` and the page in `index.html`.');
  await demo.idle();
  await demo.send('Check the login.');
  await poll(async () => !!(await demo.status()).login_required, 'the login card');
  await page.getByRole('button', {name: 'Check again'}).waitFor();
  await page.locator('[data-login-where]').evaluate(node => { node.textContent = 'on dev-machine as dev'; });
  await shot(page, 'login-card.png');
  await page.context().close();

  // TTY mode, on bash run as a PTY agent. Keys typed in TTY mode go straight to bash. (Without an
  // input box on its screen, bash never counts as ready for messages, so none are sent to it.)
  const tty = await openPage(browser, pty.base);
  await tty.getByRole('button', {name: 'Switch to TTY mode'}).click();
  const ttyRows = tty.locator('.xterm-rows');
  await ttyRows.waitFor();
  await poll(async () => (await ttyRows.innerText()).includes('my-app $'), 'the bash prompt in TTY mode');
  await tty.keyboard.type('git log --oneline\n');
  await tty.waitForTimeout(400);
  await tty.keyboard.type('git status --short\n');
  await poll(async () => (await ttyRows.innerText()).includes('styles.css'), 'git status in TTY mode');
  await tty.waitForTimeout(500);
  await shot(tty, 'tty-mode.png');

  if (errors.length) throw new Error(errors.join('\n'));
  await writeFile(path.join(output, 'capture.json'), JSON.stringify({
    captured_at: new Date().toISOString(),
    agentapi_version: version,
    agents: ['native-demo', 'native-demo posing as Claude Code', 'bash (PTY)'],
    browser: browser.version(),
    desktop_viewport: {width: 1440, height: 1000},
    mobile_viewport: {width: 390, height: 844},
    images,
  }, null, 2) + '\n');
  console.log(`Captured ${images.length} views of AgentAPI ${version} with no browser errors.`);
} catch (error) {
  for (const server of servers) {
    if (server.log()) console.error(`--- ${server.name} log ---\n${server.log()}`);
  }
  throw error;
} finally {
  await browser?.close();
  for (const server of servers) {
    server.child.kill('SIGTERM');
    const timer = setTimeout(() => server.child.kill('SIGKILL'), 8000);
    await server.exited;
    clearTimeout(timer);
  }
  await rm(root, {recursive: true, force: true});
}
