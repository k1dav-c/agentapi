/**
 * What the scripts that capture the documentation's images and videos share: Playwright, the
 * binary, the sample project the agent works on, the servers they start and stop, a client of the
 * AgentAPI they drive, the pointer recorded pages show, and a hub watching nine demo instances. See
 * docs/screenshots/README.md.
 */
import path from 'node:path';
import os from 'node:os';
import net from 'node:net';
import {spawn, execFileSync} from 'node:child_process';
import {mkdir, writeFile} from 'node:fs/promises';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {createRequire} from 'node:module';

const moduleName = process.env.AGENTAPI_PLAYWRIGHT_MODULE;
if (!moduleName) {
  console.error('Set AGENTAPI_PLAYWRIGHT_MODULE to the path of playwright-core\'s index.mjs; see docs/screenshots/README.md.');
  process.exit(1);
}
export const {chromium} = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
// Packages installed next to playwright-core, such as gifenc and pngjs for the GIFs.
export const requireBeside = createRequire(path.isAbsolute(moduleName) ? moduleName : fileURLToPath(import.meta.url));
export const repo = fileURLToPath(new URL('..', import.meta.url));
export const binary = path.resolve(process.env.AGENTAPI_BINARY || path.join(repo, 'out', 'agentapi'));
// An optional binary stamped with an older version, for one of the hub's instances, so that the hub
// shows one older than itself.
export const olderBinary = process.env.AGENTAPI_OLDER_BINARY ? path.resolve(process.env.AGENTAPI_OLDER_BINARY) : binary;

// The sample project the agent "works on": a static page with a stylesheet, a script, release
// notes with an image, two commits and uncommitted changes for the Diff tab.
export const projectFiles = {
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
export const notes = `# Release notes

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
export const createProject = async (dir, home) => {
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

export const portFree = p => new Promise(resolve => {
  const socket = net.connect({host: '127.0.0.1', port: p});
  socket.once('connect', () => { socket.destroy(); resolve(false); });
  socket.once('error', () => resolve(true));
});


export const servers = [];
// seedClaudeLog writes a Claude Code session log for cwd under home, with
// calls model calls over the last week (more of them lately), so the
// summary has a spend to show.
export async function seedClaudeLog(home, cwd, calls) {
  const dir = path.join(home, '.claude', 'projects', cwd.replace(/[^A-Za-z0-9]/g, '-'));
  await mkdir(dir, {recursive: true});
  const session = '11111111-1111-4111-8111-' + String(calls).padStart(12, '0');
  const at = (hoursAgo) => new Date(Date.now() - hoursAgo * 3600_000).toISOString();
  const lines = [{type: 'user', uuid: 'u0', sessionId: session, cwd, timestamp: at(160), message: {role: 'user', content: 'Work on the service'}}];
  for (let i = 0; i < calls; i++) {
    lines.push({type: 'assistant', uuid: `a${i}`, requestId: `req_${i}`, sessionId: session, cwd, timestamp: at(160 * (i / calls) ** 2),
      message: {model: 'claude-opus-5-5', id: `msg_${i}`, type: 'message', role: 'assistant', content: [{type: 'text', text: 'ok'}],
        usage: {input_tokens: 2000, cache_read_input_tokens: 400000, cache_creation_input_tokens: 20000, output_tokens: 8000}}});
  }
  await writeFile(path.join(dir, `${session}.jsonl`), lines.map(line => JSON.stringify(line)).join('\n') + '\n');
}

export const startServer = (name, cwd, env, args, program = binary) => startProcess(name, cwd, env,
  ['server', '--bind', '127.0.0.1', '--allowed-hosts', 'localhost,127.0.0.1', ...args], program);
export const startProcess = (name, cwd, env, args, program = binary) => {
  const child = spawn(program, args, {
    cwd, env, stdio: ['ignore', 'ignore', 'pipe'],
  });
  let log = '';
  child.stderr.on('data', chunk => { log = (log + chunk).slice(-4000); });
  const exited = new Promise(resolve => child.once('exit', resolve));
  servers.push({name, child, exited, log: () => log});
};

export const poll = async (check, label, ms = 40_000) => {
  const deadline = Date.now() + ms;
  do {
    if (await check().catch(() => false)) return;
    await new Promise(resolve => setTimeout(resolve, 150));
  } while (Date.now() < deadline);
  throw new Error(`Timed out waiting for ${label}`);
};
export const client = base => {
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

// GIFs and videos. Headless Chromium draws no mouse pointer, so the pages recorded get one that
// follows the mouse and shows clicks.
export const pointerScript = () => {
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
// Moves the pointer to the middle of locator in steps, so a recording shows where it goes, and
// clicks.
export const tap = async (page, locator) => {
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
export const typeSlowly = (page, text) => page.keyboard.type(text, {delay: 28});

export const contextOptions = {deviceScaleFactor: 1, colorScheme: 'light', locale: 'en-US', timezoneId: 'UTC', reducedMotion: 'reduce'};

// A hub watching nine demo instances, which join it as agentapi hub join has them do, posing as the
// agents they'd be: one asks for an approval, one asks questions, one needs a login, one hit its
// usage limit, one works, one finished a task nobody saw, one is idle, one stopped and one was
// killed, so the hub can't reach it. The one that asks questions runs the older binary, if there is
// one. The hub listens on hubPort and the instances on the nine ports from instancePort.
export const hubSetup = [
  {name: 'checkout-api', type: 'claude', group: 'Client work', message: 'Deploy the release: approve', wait: 'approval', calls: 60},
  {name: 'marketing-site', type: 'codex', group: 'Client work', message: 'ask me about the launch copy', wait: 'question', older: true},
  {name: 'mobile-app', type: 'codex', group: 'Client work', message: 'Check the login'},
  {name: 'data-pipeline', type: 'claude', group: 'Internal', message: 'Rebuild the nightly aggregates; limit', calls: 180},
  {name: 'billing-service', type: 'claude', group: 'Internal', calls: 25},
  {name: 'design-system', type: 'pi', group: 'Internal', message: 'Refactor the button tokens and update the docs', idle: true},
  {name: 'docs', type: 'custom'},
  {name: 'legacy-cms', type: 'custom'},
  {name: 'prototype-2025', type: 'custom'},
];
export const hubToken = 'docs-token';
export const hubSecret = 'docs-secret';
export async function startDemoHub({root, env, hubPort, instancePort}) {
  const hubHome = path.join(root, 'hub-home');
  await mkdir(hubHome, {recursive: true});
  const hubEnv = {...env, HOME: hubHome};
  // The hub's file as a hub that has watched them for a week keeps it: how long each one worked
  // (hours in the last 24 hours, and in the 6 days before), which a demo can't run that long. The
  // hub knows each instance by its ID, <hostname>:<port>, and takes it over when it joins.
  const hubWork = {
    'checkout-api': [1.6, 7.8], 'marketing-site': [0.75, 2.4], 'mobile-app': [0.3, 1.8], 'data-pipeline': [4.2, 9.8],
    'billing-service': [2.4, 8.6], 'design-system': [0.5, 1], docs: [0, 0.8], 'legacy-cms': [1.1, 4.9], 'prototype-2025': [0, 4],
  };
  const seededAt = Math.floor(Date.now() / 1000);
  const ago = hours => seededAt - Math.round(hours * 3600);
  await writeFile(path.join(hubHome, 'instances.json'), JSON.stringify({version: 1, instances: hubSetup.map((instance, i) => {
    const [day, before] = hubWork[instance.name];
    const spans = [];
    if (before > 0) spans.push([ago(72 + before), ago(72)]);
    if (day > 0) spans.push([ago(3 + day), ago(3)]);
    return {id: `${os.hostname()}:${instancePort + i}`, url: `http://127.0.0.1:${instancePort + i}`, source: 'webhook',
      last_seen: new Date(seededAt * 1000).toISOString(), changed_at: new Date(seededAt * 1000).toISOString(), work: {spans}};
  })}), {mode: 0o600});
  startProcess('hub', root, hubEnv, ['hub', 'serve', '--bind', '127.0.0.1', '--port', String(hubPort),
    '--token', hubToken, '--secret', hubSecret, '--state', path.join(hubHome, 'instances.json')]);
  await poll(async () => (await fetch(`http://127.0.0.1:${hubPort}/api/session`)).ok, 'the hub');
  // After the hub runs: join checks that it takes the events.
  execFileSync(binary, ['hub', 'join', `http://127.0.0.1:${hubPort}/hooks`, '--secret', hubSecret,
    '--public-url', 'http://127.0.0.1:{port}'], {env: hubEnv, stdio: 'ignore'});
  const instances = {};
  for (const [i, instance] of hubSetup.entries()) {
    const dir = path.join(root, 'workspaces', instance.name);
    await mkdir(dir, {recursive: true});
    if (instance.calls) await seedClaudeLog(hubHome, dir, instance.calls);
    startServer(instance.name, dir, hubEnv, ['--port', String(instancePort + i), '--type', instance.type, '--', 'native-demo'],
      instance.older ? olderBinary : binary);
    instances[instance.name] = client(`http://127.0.0.1:${instancePort + i}`);
    await poll(async () => (await instances[instance.name].status()).lifecycle === 'ready', `the ${instance.name} instance`);
  }
  const api = `http://127.0.0.1:${hubPort}`;
  const request = (route, method = 'GET', body) => fetch(`${api}${route}`, {method,
    headers: {Authorization: `Bearer ${hubToken}`, ...(body === undefined ? {} : {'Content-Type': 'application/json'})},
    body: body === undefined ? undefined : JSON.stringify(body)});
  const list = async () => (await (await request('/api/instances')).json()).instances;
  const patch = async (name, body) => {
    const instance = (await list()).find(entry => entry.name === name);
    await request(`/api/instances/${encodeURIComponent(instance.id)}`, 'PATCH', body);
  };
  await poll(async () => (await list()).filter(instance => instance.summary).length === hubSetup.length, 'the hub to follow the instances');
  if ((await list()).some(instance => !instance.work?.last_7d_seconds)) throw new Error('the hub lost the seeded work time: the instances joined with other IDs');
  // Groups of one's own, and two agents pinned to the top, as the rows' menus set them.
  for (const instance of hubSetup) {
    if (instance.group) await patch(instance.name, {group: instance.group});
  }
  for (const name of ['checkout-api', 'billing-service']) await patch(name, {pinned: true});
  // Favorite prompts kept on the hub, as its settings add them.
  await request('/api/favorites', 'PUT', {favorites: [
    {title: 'Review the diff', text: 'Review the diff on this branch.\nList the risks first, then the nits.'},
    {title: 'Tests first', text: 'Write a failing test for the bug, then make it pass.'},
    {text: 'Explain this code step by step, as to a new teammate.'},
    {title: 'Release notes', text: 'Draft the release notes from the commits since the last tag.'},
  ]});
  const stopInstance = async (name, signal) => {
    const server = servers.find(entry => entry.name === name);
    server.child.kill(signal);
    await server.exited;
  };
  await stopInstance('prototype-2025', 'SIGTERM');
  await stopInstance('legacy-cms', 'SIGKILL');
  for (const instance of hubSetup) {
    if (!instance.message) continue;
    if (instance.idle) {
      await instances[instance.name].send(instance.message);
      await instances[instance.name].idle();
    } else {
      await instances[instance.name].request('/message', 'POST', {content: instance.message, type: 'user'});
    }
  }
  await instances['checkout-api'].pending('approval');
  await instances['marketing-site'].pending('question');
  await poll(async () => {
    const byName = Object.fromEntries((await list()).map(instance => [instance.name, instance]));
    return byName['prototype-2025'].connection === 'stopped' && byName['legacy-cms'].connection === 'unreachable' &&
      byName['mobile-app'].summary?.attention === 'login' && byName['data-pipeline'].summary?.attention === 'limit';
  }, 'the hub to see every state');
  // One agent at work, for the overview.
  const startWork = () => instances['billing-service'].request('/message', 'POST', {content: 'long migration of the invoices table', type: 'user'});
  return {api, home: hubHome, env: hubEnv, instances, list, patch, startWork};
}

// The demo's instances all run on this machine. The hub's page gets them as if they ran on three,
// named dev-1 to dev-3, for By machine, the overview's cards and the bar's switch of a machine's
// agents: the hub's events name another machine for each before the page reads them, so the
// captures show these placeholders. dev-2 has agents in both groups and in none.
export const hubMachines = {
  'checkout-api': 'dev-1', 'marketing-site': 'dev-1', 'mobile-app': 'dev-1',
  'data-pipeline': 'dev-2', 'billing-service': 'dev-2', 'design-system': 'dev-2', 'prototype-2025': 'dev-2',
  docs: 'dev-3', 'legacy-cms': 'dev-3',
};
export const machinesScript = machines => {
  const move = instance => { if (instance && machines[instance.name]) instance.hostname = machines[instance.name]; };
  window.EventSource = class extends window.EventSource {
    addEventListener(type, listener, options) {
      if (type !== 'instances' && type !== 'instance') return super.addEventListener(type, listener, options);
      return super.addEventListener(type, event => {
        const data = JSON.parse(event.data);
        if (type === 'instances') (data.instances ?? []).forEach(move);
        else move(data);
        listener.call(this, new MessageEvent(event.type, {data: JSON.stringify(data), lastEventId: event.lastEventId}));
      }, options);
    }
  };
};
