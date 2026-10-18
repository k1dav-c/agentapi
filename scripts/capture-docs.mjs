/** Capture the documentation screenshots from a dedicated native-demo server; see docs/screenshots/README.md. */
import path from 'node:path';
import {mkdir, writeFile} from 'node:fs/promises';
import {fileURLToPath, pathToFileURL} from 'node:url';

const moduleName = process.env.AGENTAPI_PLAYWRIGHT_MODULE;
if (!moduleName) {
  console.error('Set AGENTAPI_PLAYWRIGHT_MODULE to the path of playwright-core\'s index.mjs; see docs/screenshots/README.md.');
  process.exit(1);
}
const {chromium} = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const ui = process.env.AGENTAPI_SCREENSHOT_UI || 'http://127.0.0.1:3311/chat/';
const api = process.env.AGENTAPI_SCREENSHOT_API || 'http://127.0.0.1:3311';
const output = fileURLToPath(new URL('../docs/screenshots/', import.meta.url));
await mkdir(output, {recursive: true});
const request = async (route, method = 'GET', body) => {
  const response = await fetch(`${api}${route}`, {method,
    headers: body === undefined ? undefined : {'Content-Type': 'application/json'},
    body: body === undefined ? undefined : JSON.stringify(body)});
  if (!response.ok) throw new Error(`${method} ${route}: ${response.status}`);
  const text = await response.text();
  return text ? JSON.parse(text) : undefined;
};
const status = await request('/status');
if (status.transport !== 'native' || status.agent_type !== 'custom' || status.session_name !== 'Native demo') {
  throw new Error('Use a dedicated native-demo server. Refusing to modify another session.');
}
const poll = async (check, label) => {
  const deadline = Date.now() + 40_000;
  do {
    if (await check()) return;
    await new Promise(resolve => setTimeout(resolve, 150));
  } while (Date.now() < deadline);
  throw new Error(`Timed out waiting for ${label}`);
};
const idle = () => poll(async () => {
  const state = await request('/status');
  return state.status === 'stable' && state.lifecycle === 'ready' && !state.pending_requests;
}, 'the demo to finish');
const send = async content => {
  const before = (await request('/status')).run_id;
  await request('/message', 'POST', {content, type: 'user'});
  await poll(async () => (await request('/status')).run_id > before, 'a new turn');
};
const pending = kind => poll(async () => (await request('/requests')).requests.some(r => r.kind === kind && r.status === 'pending'), `${kind} request`);
const screenshotStyle = 'nextjs-portal { visibility: hidden !important; }';
const screenshotOptions = name => ({path: path.join(output, name), animations: 'disabled', style: screenshotStyle});
let browser;
try {
  await request('/messages', 'DELETE');await idle();
  await send('Review `README.md` and explain the native demo.');await idle();
  browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || undefined,
    args: ['--no-sandbox', '--disable-dev-shm-usage']});
  const contextOptions = {deviceScaleFactor: 1, colorScheme: 'light', locale: 'en-US', timezoneId: 'UTC', reducedMotion: 'reduce'};
  const context = await browser.newContext({...contextOptions, viewport: {width: 1440, height: 1000}});
  await context.addInitScript(({origin}) => {
    if (window === window.top && location.origin === origin) localStorage.setItem('theme', 'light');
  }, {origin: new URL(ui).origin});
  const page = await context.newPage();page.setDefaultTimeout(20_000);
  const errors = [];
  context.on('weberror', event => errors.push(event.error().message));
  const url = new URL(ui);url.searchParams.set('url', api);
  const ready = async target => {
    await target.getByRole('textbox', {name: 'Task message'}).waitFor();
    await target.getByText('Here is what I did for', {exact: false}).first().waitFor();
    await target.evaluate(() => document.fonts.ready);
    await target.waitForTimeout(800);
  };
  const capture = async (name, target = page) => {
    await page.mouse.move(0, 0);await page.waitForTimeout(250);
    await target.screenshot(screenshotOptions(name));console.log(`Captured ${name}`);
  };
  const navigation = await page.goto(url.href);
  if (navigation && !navigation.ok()) throw new Error(`Frontend returned HTTP ${navigation.status()}; complete any pending UI edits before capturing.`);
  await ready(page);
  await capture('chat-desktop.png');
  await page.locator('summary').filter({hasText: /^\$?\s*ls/}).first().click();
  await capture('tool-call.png', page.locator('details').filter({has: page.locator('summary').filter({hasText: /^\$?\s*ls/})}).first());
  await page.locator('summary').filter({hasText: /^\$?\s*ls/}).first().click();
  await page.locator('button[title="Model and reasoning effort"]').click();
  await page.getByRole('menuitemradio', {name: /Swift/}).waitFor();
  await capture('model-settings.png');await page.keyboard.press('Escape');
  await page.getByRole('button', {name: 'Open explorer'}).click();
  await page.getByRole('tab', {name: 'Index', exact: true}).click();
  await capture('explorer-index.png', page.getByRole('dialog'));
  await page.keyboard.press('Escape');
  await page.getByRole('button', {name: 'Open file: README.md', exact: true}).click();
  await page.getByRole('tab', {name: 'Preview', exact: true}).waitFor();
  await page.getByRole('tab', {name: 'File', exact: true}).click();
  await page.getByRole('dialog').last().getByRole('tabpanel').filter({hasText: 'English'}).waitFor();
  await capture('file-viewer.png', page.getByRole('dialog').last());
  await page.keyboard.press('Escape');await page.keyboard.press('Escape');
  const mobile = await browser.newContext({...contextOptions, viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true});
  mobile.on('weberror', event => errors.push(event.error().message));
  await mobile.addInitScript(({origin}) => {
    if (window === window.top && location.origin === origin) localStorage.setItem('theme', 'light');
  }, {origin: new URL(ui).origin});
  const phone = await mobile.newPage();phone.setDefaultTimeout(20_000);
  await phone.goto(url.href);await ready(phone);
  await phone.screenshot(screenshotOptions('chat-mobile.png'));console.log('Captured chat-mobile.png');
  await mobile.close();
  await send('ask which checks to run before updating the documentation.');await pending('question');
  await page.locator('[data-request-kind="question"]').waitFor();await capture('native-question.png');
  await request('/interrupt', 'POST');await idle();
  await send('plan the documentation update.');await pending('plan');
  await page.locator('[data-request-kind="plan"]').waitFor();await capture('native-plan.png');
  await request('/interrupt', 'POST');await idle();
  if (errors.length) throw new Error(errors.join('\n'));
  await writeFile(path.join(output, 'capture.json'), JSON.stringify({
    captured_at: new Date().toISOString(),
    agentapi_version: status.agentapi_version,
    ui: ui,
    api: api,
    agent: 'native-demo',
    browser: await browser.version(),
    desktop_viewport: {width: 1440, height: 1000},
    mobile_viewport: {width: 390, height: 844},
    images: ['chat-desktop.png', 'tool-call.png', 'model-settings.png', 'explorer-index.png',
      'file-viewer.png', 'chat-mobile.png', 'native-question.png', 'native-plan.png'],
  }, null, 2) + '\n');
  console.log('Captured all eight views with no browser errors.');
} finally {
  await browser?.close();
}
