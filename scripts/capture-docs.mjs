/**
 * Captures the documentation screenshots in docs/screenshots/. The script starts its own AgentAPI
 * servers on a small sample project (the native demo agent, and bash as a PTY agent for TTY mode),
 * walks the chat UI with Chromium and stops the servers again. See docs/screenshots/README.md.
 */
import path from 'node:path';
import os from 'node:os';
import net from 'node:net';
import {spawn, execFileSync} from 'node:child_process';
import {mkdtemp, mkdir, writeFile, rm} from 'node:fs/promises';
import {fileURLToPath, pathToFileURL} from 'node:url';

const moduleName = process.env.AGENTAPI_PLAYWRIGHT_MODULE;
if (!moduleName) {
  console.error('Set AGENTAPI_PLAYWRIGHT_MODULE to the path of playwright-core\'s index.mjs; see docs/screenshots/README.md.');
  process.exit(1);
}
const {chromium} = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
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
    const before = (await status()).run_id;
    await request('/message', 'POST', {content, type: 'user'});
    await poll(async () => (await status()).run_id > before, 'a new turn');
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
  for (const p of [port, port + 1]) {
    if (!await portFree(p)) throw new Error(`Port ${p} is in use; set AGENTAPI_SCREENSHOT_PORT to a free pair of ports.`);
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
  const demo = client(`http://127.0.0.1:${port}`);
  const pty = client(`http://127.0.0.1:${port + 1}`);
  await poll(async () => (await demo.status()).lifecycle === 'ready', 'the native demo server');
  await poll(async () => !!(await pty.status()), 'the PTY server');
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
    agents: ['native-demo', 'bash (PTY)'],
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
