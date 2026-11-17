/**
 * Records the overview video in docs/videos/: Tendyard's hub, Issues, chat, phone, queue and API in
 * about a minute and a half, once with English captions and once with Traditional Chinese ones.
 * The script starts the same demo servers as capture-docs.mjs (the chat's demo posing as Claude
 * Code, and a hub watching nine demo instances), records each live scene from Chromium's
 * screencast, and has ffmpeg put each one on a slide with its caption. The Issues scenes are the
 * screenshots capture-dispatch.mjs and github.com gave. See docs/screenshots/README.md.
 */
import path from 'node:path';
import os from 'node:os';
import {execFileSync} from 'node:child_process';
import {mkdtemp, mkdir, writeFile, readFile, rm} from 'node:fs/promises';
import {
  chromium, repo, createProject, portFree, servers, startServer, poll, client, pointerScript, tap, typeSlowly,
  contextOptions, hubToken, startDemoHub, hubMachines, machinesScript,
} from './capture-common.mjs';

const ffmpeg = process.env.FFMPEG || 'ffmpeg';
const port = Number(process.env.AGENTAPI_VIDEO_PORT || 3330);
const output = path.join(repo, 'docs', 'videos');
const screenshots = path.join(repo, 'docs', 'screenshots');

// The video is 1920 × 1080 at 30 frames a second. A desktop scene is a 1440 × 810 window shown at
// 1600 × 900 under a window bar, its caption below; a phone scene is a 390 × 844 screen shown at
// 468 × 1013 in a bezel, its caption beside it.
const width = 1920;
const height = 1080;
const fps = 30;
const desktop = {viewport: {width: 1440, height: 810}, scale: 1600 / 1440, slot: {x: 160, y: 64, width: 1600, height: 900}};
const phone = {viewport: {width: 390, height: 844}, scale: 1.2, slot: {x: 330, y: 34, width: 468, height: 1013}};
// How long the last frame of a live scene stays, and the fades between scenes, in seconds.
const hold = 1.2;
const fade = 0.3;

// The scenes in order. A live scene is recorded, and plays faster by its speed; a still shows a
// screenshot; title, api and end are slides only. Each caption has its chapter and its line in both
// languages.
const scenes = [
  {id: 'title', kind: 'title', seconds: 4.5},
  {id: 'hub', kind: 'desktop', url: 'localhost:3290',
    chapter: {en: 'Hub', 'zh-TW': 'Hub'},
    caption: {en: 'Every agent on every machine in one page, with what needs you first',
      'zh-TW': '每台主機上的 agent 都在同一頁，需要你處理的排在最前面'}},
  {id: 'issues', kind: 'still', image: 'dispatch-issues.png', url: 'localhost:3290/issues', seconds: 4,
    chapter: {en: 'Issues', 'zh-TW': 'Issues'},
    caption: {en: 'Assign a GitHub issue to Claude Code, Codex or Pi on a machine you pick',
      'zh-TW': '把 GitHub issue 指派給你選的主機上的 Claude Code、Codex 或 Pi'}},
  {id: 'issue-detail', kind: 'still', image: 'dispatch-detail.png', url: 'localhost:3290/issues', seconds: 4,
    chapter: {en: 'Issues', 'zh-TW': 'Issues'},
    caption: {en: 'Each agent works in its own worktree; follow its runs, spend and time',
      'zh-TW': '每個 agent 在自己的 worktree 工作，執行紀錄、花費與時間一目了然'}},
  {id: 'review', kind: 'still', image: 'github-pr-review.png', url: 'github.com', seconds: 4.5, backdrop: '#ffffff',
    chapter: {en: 'Issues', 'zh-TW': 'Issues'},
    caption: {en: 'It opens a pull request, and your review goes back to the same agent',
      'zh-TW': 'agent 開出 pull request，你的 review 意見會回到同一個 agent'}},
  {id: 'task', kind: 'desktop', url: 'localhost:3284/chat',
    chapter: {en: 'Chat', 'zh-TW': '聊天'},
    caption: {en: 'Send a task and watch the agent think, run tools and reply',
      'zh-TW': '送出任務，看 agent 思考、執行工具、回覆'}},
  {id: 'approval', kind: 'desktop', url: 'localhost:3284/chat', speed: 1.2,
    chapter: {en: 'Chat', 'zh-TW': '聊天'},
    caption: {en: 'Approve a command right in the chat',
      'zh-TW': '直接在對話中核准指令'}},
  {id: 'files', kind: 'desktop', url: 'localhost:3284/chat', speed: 1.3,
    chapter: {en: 'Chat', 'zh-TW': '聊天'},
    caption: {en: 'Open the files it touched, preview a page, read the diff',
      'zh-TW': '開啟 agent 改過的檔案、預覽網頁、查看 diff'}},
  {id: 'phone', kind: 'phone',
    chapter: {en: 'Phone', 'zh-TW': '手機'},
    caption: {en: 'Answer it from your phone', 'zh-TW': '用手機也能回覆'},
    detail: {en: 'Open the agent that needs you, allow its command, and it leaves the list of those waiting.',
      'zh-TW': '打開需要你的 agent，允許它的指令，它就離開等待清單。'}},
  {id: 'queue', kind: 'desktop', url: 'localhost:3284/chat', speed: 1.6,
    chapter: {en: 'Long-running work', 'zh-TW': '長時間工作'},
    caption: {en: 'Queue the next task, or steer the one that is running',
      'zh-TW': '排入下一個任務，或 steer 進行中的回合'}},
  {id: 'limit', kind: 'still', image: 'limit-card.png', url: 'localhost:3284/chat', seconds: 4,
    chapter: {en: 'Long-running work', 'zh-TW': '長時間工作'},
    caption: {en: 'A usage limit holds the queue until the reset, then the work goes on',
      'zh-TW': '遇到用量限制時暫停佇列，重置後繼續工作'}},
  {id: 'api', kind: 'api', seconds: 5,
    chapter: {en: 'API', 'zh-TW': 'API'},
    caption: {en: 'Scripts and bots drive the same agents over HTTP and webhooks',
      'zh-TW': '腳本與機器人透過 HTTP 和 webhook 操作同一批 agent'}},
  {id: 'end', kind: 'end', seconds: 5},
];
const languages = {
  en: {file: 'tendyard-overview.mp4', poster: 'tendyard-overview.png', lang: 'en', watch: 'Watch the overview',
    tagline: 'Run Claude Code, Codex and Pi on your own machines, from an issue to a reviewed pull request.',
    demo: 'Recorded with the built-in demo agent; its replies are simulated.',
    start: 'Get started'},
  'zh-TW': {file: 'tendyard-overview.zh-TW.mp4', poster: 'tendyard-overview.zh-TW.png', lang: 'zh-Hant-TW', watch: '觀看功能導覽',
    tagline: '在自己的主機上執行 Claude Code、Codex 與 Pi，從 issue 一路到完成 review 的 pull request。',
    demo: '以內建的 demo agent 錄製，回覆內容為模擬。',
    start: '開始使用'},
};

// The slides: everything but the recorded screen, drawn by Chromium so both languages get proper
// type. A live scene's slide is transparent where its screen goes.
const slideCSS = `
  * { box-sizing: border-box; margin: 0; }
  html, body { width: ${width}px; height: ${height}px; background: transparent; }
  body { font-family: 'Noto Sans TC', 'Noto Sans', sans-serif; color: #e2e8f0; position: relative; overflow: hidden; }
  .bg { position: absolute; inset: 0; background: radial-gradient(1200px 700px at 20% 0%, #1e3a8a 0%, transparent 60%), #0b1220; }
  .hole { position: absolute; background: transparent; box-shadow: 0 0 0 4000px #0b1220; }
  .bar { position: absolute; display: flex; align-items: center; gap: 10px; padding: 0 18px; background: #1e293b;
    border-radius: 14px 14px 0 0; height: 40px; }
  .dot { width: 13px; height: 13px; border-radius: 50%; background: #475569; }
  .url { margin-left: 18px; flex: 1; max-width: 560px; height: 26px; border-radius: 13px; background: #0f172a; color: #94a3b8;
    font: 15px/26px ui-monospace, 'DejaVu Sans Mono', monospace; padding: 0 14px; }
  .caption { position: absolute; left: 160px; right: 160px; display: flex; align-items: center; gap: 22px; }
  .chapter { flex: none; padding: 6px 16px; border-radius: 999px; background: #2563eb; color: #fff; font-size: 22px; font-weight: 700;
    letter-spacing: .02em; }
  .line { font-size: 36px; font-weight: 600; color: #f8fafc; line-height: 1.25; }
  .shot { position: absolute; overflow: hidden; border-radius: 0 0 14px 14px; display: flex; align-items: center; justify-content: center; }
  .shot img { max-width: 100%; max-height: 100%; }
  .bezel { position: absolute; border-radius: 56px; border: 12px solid #1e293b; box-shadow: 0 30px 80px rgb(0 0 0 / .5); }
  .side { position: absolute; left: 900px; right: 160px; top: 0; bottom: 0; display: flex; flex-direction: column; justify-content: center; gap: 28px; }
  .side .line { font-size: 54px; line-height: 1.2; }
  .side .detail { font-size: 30px; color: #cbd5e1; line-height: 1.5; }
  .center { position: absolute; inset: 0; display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 30px; text-align: center; padding: 0 200px; }
  .name { font-size: 132px; font-weight: 800; letter-spacing: -.02em; color: #fff; }
  .tagline { font-size: 40px; line-height: 1.45; color: #cbd5e1; font-weight: 500; }
  .agents { display: flex; gap: 14px; }
  .agents span { padding: 8px 20px; border-radius: 999px; border: 2px solid #334155; font-size: 26px; color: #e2e8f0; }
  .note { position: absolute; bottom: 48px; left: 0; right: 0; text-align: center; font-size: 22px; color: #64748b; }
  .repo { font: 600 44px/1 ui-monospace, 'DejaVu Sans Mono', monospace; color: #93c5fd; }
  .code { width: 1240px; text-align: left; background: #020617; border: 2px solid #1e293b; border-radius: 18px; padding: 40px 48px;
    font: 30px/1.6 ui-monospace, 'DejaVu Sans Mono', monospace; color: #e2e8f0; white-space: pre; }
  .code .c { color: #64748b; } .code .s { color: #86efac; } .code .k { color: #93c5fd; }
  .play { position: absolute; left: 50%; top: 50%; transform: translate(-50%, -50%); display: flex; align-items: center; gap: 28px;
    padding: 26px 48px 26px 30px; border-radius: 999px; background: rgb(11 18 32 / .88); box-shadow: 0 20px 60px rgb(0 0 0 / .45); }
  .play i { width: 96px; height: 96px; border-radius: 50%; background: #2563eb; display: grid; place-items: center; }
  .play i::after { content: ''; margin-left: 10px; border-style: solid; border-width: 24px 0 24px 38px; border-color: transparent transparent transparent #fff; }
  .play span { font-size: 46px; font-weight: 700; color: #fff; }
`;
const escape = text => text.replace(/[&<>"]/g, c => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;'})[c]);
const windowBar = (slot, url) => `<div class="bar" style="left:${slot.x}px;top:${slot.y - 40}px;width:${slot.width}px">
  <span class="dot"></span><span class="dot"></span><span class="dot"></span><span class="url">${escape(url)}</span></div>`;
const captionRow = (scene, language, top) => `<div class="caption" style="top:${top}px">
  <span class="chapter">${escape(scene.chapter[language])}</span><span class="line">${escape(scene.caption[language])}</span></div>`;
// A still's screenshot goes into its slide as a data: URL, since the slide page may not load files.
const slideHTML = (scene, language) => {
  const text = languages[language];
  const {slot} = scene.kind === 'phone' ? phone : desktop;
  let body;
  switch (scene.kind) {
  case 'title':
    body = `<div class="bg"></div><div class="center"><div class="name">Tendyard</div>
      <div class="tagline">${escape(text.tagline)}</div>
      <div class="agents"><span>Claude Code</span><span>Codex</span><span>Pi</span></div></div>`;
    break;
  case 'end':
    body = `<div class="bg"></div><div class="center"><div class="name">Tendyard</div>
      <div class="tagline">${escape(text.start)}</div><div class="repo">github.com/k1dav-c/agentapi</div></div>
      <div class="note">${escape(text.demo)}</div>`;
    break;
  case 'api':
    body = `<div class="bg"></div><div class="center" style="bottom:110px">
      <div class="code"><span class="c"># Send the agent a task</span>
<span class="k">curl</span> -X POST localhost:3284/message \\
  -H <span class="s">"Content-Type: application/json"</span> \\
  -d <span class="s">'{"content": "Summarize this repository", "type": "user"}'</span>

<span class="c"># Follow its work as it happens</span>
<span class="k">curl</span> -N localhost:3284/events</div></div>
      ${captionRow(scene, language, 940)}`;
    break;
  case 'still':
    body = `<div class="bg"></div>${windowBar(slot, scene.url)}
      <div class="shot" style="left:${slot.x}px;top:${slot.y}px;width:${slot.width}px;height:${slot.height}px;background:${scene.backdrop ?? '#ffffff'}">
      <img src="${scene.dataURL}"></div>
      ${captionRow(scene, language, slot.y + slot.height + 26)}`;
    break;
  case 'desktop':
    body = `<div class="hole" style="left:${slot.x}px;top:${slot.y}px;width:${slot.width}px;height:${slot.height}px;border-radius:0 0 14px 14px"></div>
      ${windowBar(slot, scene.url)}${captionRow(scene, language, slot.y + slot.height + 26)}`;
    break;
  case 'phone':
    body = `<div class="hole" style="left:${slot.x}px;top:${slot.y}px;width:${slot.width}px;height:${slot.height}px;border-radius:44px"></div>
      <div class="bezel" style="left:${slot.x - 12}px;top:${slot.y - 12}px;width:${slot.width + 24}px;height:${slot.height + 24}px"></div>
      <div class="side"><span class="chapter" style="align-self:flex-start">${escape(scene.chapter[language])}</span>
      <div class="line">${escape(scene.caption[language])}</div><div class="detail">${escape(scene.detail[language])}</div></div>`;
    break;
  }
  return slidePage(text, body);
};
const slidePage = (text, body) => `<!doctype html><html lang="${text.lang}"><head><meta charset="utf-8"><style>${slideCSS}</style></head><body>${body}</body></html>`;
// The poster the READMEs link to the video with: a frame of the hub scene under a play button.
const posterHTML = language => slidePage(languages[language], `<div class="play"><i></i><span>${escape(languages[language].watch)}</span></div>`);

// Records what action does on page: Chromium's screencast sends a frame each time the page
// changes, with its time, which the scene's frame list keeps as how long each frame shows.
const recordScene = async (page, scene, dir, action) => {
  const frames = [];
  const cdp = await page.context().newCDPSession(page);
  cdp.on('Page.screencastFrame', frame => {
    frames.push({at: frame.metadata.timestamp, data: frame.data});
    cdp.send('Page.screencastFrameAck', {sessionId: frame.sessionId}).catch(() => {});
  });
  await cdp.send('Page.startScreencast', {format: 'jpeg', quality: 92, everyNthFrame: 1});
  await page.waitForTimeout(600);
  await action();
  await page.waitForTimeout(400);
  await cdp.send('Page.stopScreencast');
  await cdp.detach();
  const sceneDir = path.join(dir, scene.id);
  await mkdir(sceneDir, {recursive: true});
  const speed = scene.speed ?? 1;
  let list = '';
  for (const [i, frame] of frames.entries()) {
    const file = path.join(sceneDir, `${String(i).padStart(5, '0')}.jpg`);
    await writeFile(file, Buffer.from(frame.data, 'base64'));
    const next = frames[i + 1];
    list += `file '${file}'\nduration ${(next ? (next.at - frame.at) / speed : hold).toFixed(4)}\n`;
  }
  // The concat demuxer takes the last duration only from a file listed once more.
  list += `file '${path.join(sceneDir, `${String(frames.length - 1).padStart(5, '0')}.jpg`)}'\n`;
  await writeFile(path.join(sceneDir, 'frames.txt'), list);
  scene.seconds = (frames.at(-1).at - frames[0].at) / speed + hold;
  scene.frames = path.join(sceneDir, 'frames.txt');
  console.log(`Recorded ${scene.id} (${frames.length} frames, ${scene.seconds.toFixed(1)} s)`);
};

// Puts each scene on its slide and joins them, for one language.
const compose = async (dir, browser, language) => {
  const slides = await browser.newPage({viewport: {width, height}, deviceScaleFactor: 1});
  const parts = [];
  for (const scene of scenes) {
    if (scene.image && !scene.dataURL) scene.dataURL = `data:image/png;base64,${(await readFile(path.join(screenshots, scene.image))).toString('base64')}`;
    const slide = path.join(dir, `${scene.id}.${language}.png`);
    await slides.setContent(slideHTML(scene, language), {waitUntil: 'load'});
    await slides.evaluate(() => document.fonts.ready);
    await slides.screenshot({path: slide, omitBackground: true});
    const part = path.join(dir, `${scene.id}.${language}.mp4`);
    const seconds = scene.seconds.toFixed(3);
    const fades = `fade=in:st=0:d=${fade},fade=out:st=${(scene.seconds - fade).toFixed(3)}:d=${fade}`;
    const encode = ['-c:v', 'libx264', '-preset', 'slow', '-crf', '20', '-pix_fmt', 'yuv420p', '-r', String(fps), '-t', seconds, part];
    if (scene.frames) {
      const {slot} = scene.kind === 'phone' ? phone : desktop;
      execFileSync(ffmpeg, ['-v', 'error', '-y',
        '-f', 'lavfi', '-i', `color=c=0x0b1220:s=${width}x${height}:r=${fps}:d=${seconds}`,
        '-f', 'concat', '-safe', '0', '-i', scene.frames,
        '-loop', '1', '-framerate', String(fps), '-i', slide,
        '-filter_complex', `[1:v]fps=${fps},scale=${slot.width}:${slot.height}:flags=lanczos,setsar=1[screen];` +
          `[0:v][screen]overlay=${slot.x}:${slot.y}:eof_action=repeat[base];[base][2:v]overlay=0:0:shortest=1,${fades}[v]`,
        '-map', '[v]', ...encode]);
    } else {
      execFileSync(ffmpeg, ['-v', 'error', '-y', '-loop', '1', '-framerate', String(fps), '-i', slide,
        '-vf', `format=rgb24,${fades}`, ...encode]);
    }
    parts.push(part);
  }
  const play = path.join(dir, `play.${language}.png`);
  await slides.setContent(posterHTML(language), {waitUntil: 'load'});
  await slides.evaluate(() => document.fonts.ready);
  await slides.screenshot({path: play, omitBackground: true});
  await slides.close();
  const poster = path.join(output, languages[language].poster);
  execFileSync(ffmpeg, ['-v', 'error', '-y', '-ss', '5', '-i', parts[scenes.findIndex(entry => entry.id === 'hub')], '-i', play,
    '-filter_complex', '[0:v][1:v]overlay=0:0,scale=1280:-1:flags=lanczos', '-frames:v', '1', poster]);
  const list = path.join(dir, `parts.${language}.txt`);
  await writeFile(list, parts.map(part => `file '${part}'`).join('\n') + '\n');
  const file = path.join(output, languages[language].file);
  execFileSync(ffmpeg, ['-v', 'error', '-y', '-f', 'concat', '-safe', '0', '-i', list, '-c', 'copy', '-movflags', '+faststart', file]);
  console.log(`Wrote ${path.relative(repo, file)}`);
  return file;
};

const errors = [];
const newPage = async (browser, options, scripts = []) => {
  const context = await browser.newContext({...contextOptions, ...options});
  context.on('weberror', event => errors.push(event.error().message));
  await context.addInitScript(() => { try { localStorage.setItem('theme', 'light'); } catch {} });
  await context.addInitScript(pointerScript);
  for (const [script, arg] of scripts) await context.addInitScript(script, arg);
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  return page;
};

const root = await mkdtemp(path.join(os.tmpdir(), 'agentapi-video-'));
const home = path.join(root, 'home');
const project = path.join(root, 'my-app');
const frames = path.join(root, 'frames');
const browsers = [];
try {
  execFileSync(ffmpeg, ['-version'], {stdio: 'ignore'});
  for (let p = port; p < port + 11; p++) {
    if (!await portFree(p)) throw new Error(`Port ${p} is in use; set AGENTAPI_VIDEO_PORT to the first of eleven free ports.`);
  }
  await mkdir(output, {recursive: true});
  await mkdir(home, {recursive: true});
  await writeFile(path.join(home, '.bash_profile'), "PS1='my-app $ '\nexport PS1\n");
  await writeFile(path.join(home, '.hushlogin'), '');
  await createProject(project, home);
  const env = {HOME: home, PATH: '/usr/local/bin:/usr/bin:/bin', SHELL: '/bin/bash', LANG: 'C.UTF-8', TERM: 'xterm-256color'};
  // The chat's agent: the demo posing as Claude Code, as the hub's agents do.
  startServer('demo as Claude Code', project, env, ['--port', String(port), '--type', 'claude', '--', 'native-demo']);
  const demo = client(`http://127.0.0.1:${port}`);
  await poll(async () => (await demo.status()).lifecycle === 'ready', 'the demo');
  const hub = await startDemoHub({root, env, hubPort: port + 1, instancePort: port + 2});
  await hub.startWork();

  // Chromium screencasts at the device scale factor it was started with, so each layout gets its own.
  const launch = async scale => {
    const browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || undefined,
      args: ['--no-sandbox', '--disable-dev-shm-usage', `--force-device-scale-factor=${scale}`]});
    browsers.push(browser);
    return browser;
  };
  const desktopBrowser = await launch(desktop.scale);
  const phoneBrowser = await launch(phone.scale);
  const desktopOptions = {viewport: desktop.viewport, deviceScaleFactor: desktop.scale};
  const phoneOptions = {viewport: phone.viewport, deviceScaleFactor: phone.scale, isMobile: true, hasTouch: true};
  const scene = id => scenes.find(entry => entry.id === id);

  // The hub: the overview, the list by machine, and the agent that waits for an approval.
  const signIn = async page => {
    await page.goto(`${hub.api}/`);
    await page.evaluate(() => document.fonts.ready);
    await page.getByLabel('Hub token').fill(hubToken);
    await page.getByRole('button', {name: 'Sign in'}).click();
    await page.getByRole('list', {name: 'Instances'}).getByText('Approval', {exact: true}).first().waitFor();
    await page.waitForTimeout(800);
  };
  const hubPage = await newPage(desktopBrowser, desktopOptions, [[machinesScript, hubMachines]]);
  await signIn(hubPage);
  await recordScene(hubPage, scene('hub'), frames, async () => {
    await hubPage.waitForTimeout(1200);
    await hubPage.mouse.move(700, 300, {steps: 20});
    await hubPage.mouse.wheel(0, 400);
    await hubPage.waitForTimeout(1200);
    await hubPage.mouse.wheel(0, -400);
    await hubPage.waitForTimeout(500);
    await tap(hubPage, hubPage.getByRole('button', {name: 'By machine'}));
    await hubPage.getByRole('tablist', {name: 'Agents on dev-3'}).waitFor();
    await hubPage.waitForTimeout(1500);
    await tap(hubPage, hubPage.getByRole('button', {name: 'By machine'}));
    await hubPage.waitForTimeout(600);
    await tap(hubPage, hubPage.getByRole('list', {name: 'Instances'}).getByText('checkout-api'));
    await hubPage.frameLocator('iframe:not([inert])').getByRole('button', {name: 'Allow'}).first().waitFor();
    await hubPage.waitForTimeout(1800);
  });
  await hubPage.context().close();

  // The chat: a task, an approval, the files.
  await demo.idle();
  const chat = await newPage(desktopBrowser, desktopOptions);
  const chatURL = new URL(`${demo.base}/chat/`);
  chatURL.searchParams.set('url', demo.base);
  await chat.goto(chatURL.href);
  const box = chat.getByRole('textbox', {name: 'Task message'});
  await box.waitFor();
  await chat.evaluate(() => document.fonts.ready);
  await chat.waitForTimeout(600);
  const sendTask = async text => {
    await tap(chat, box);
    await typeSlowly(chat, text);
    await chat.waitForTimeout(300);
    await chat.keyboard.press('Enter');
  };
  await recordScene(chat, scene('task'), frames, async () => {
    await sendTask('Review `README.md` and the page in `index.html`.');
    await demo.idle();
    await chat.getByText('Here is what I did for', {exact: false}).last().waitFor();
    await chat.waitForTimeout(1200);
  });
  await recordScene(chat, scene('approval'), frames, async () => {
    await sendTask('Rebuild the release artifacts; I will approve the command.');
    const card = chat.locator('[data-request-kind="approval"]');
    await card.waitFor();
    await chat.waitForTimeout(1000);
    await tap(chat, card.getByRole('button', {name: /^1?\s*Allow$/}));
    await card.waitFor({state: 'detached'});
    await chat.waitForTimeout(1500);
  });
  await demo.idle();
  await recordScene(chat, scene('files'), frames, async () => {
    await tap(chat, chat.getByRole('button', {name: 'Open file: index.html', exact: true}).first());
    await chat.getByRole('tab', {name: 'Preview', exact: true}).waitFor();
    await chat.waitForTimeout(600);
    await tap(chat, chat.getByRole('tab', {name: 'Preview', exact: true}));
    await chat.waitForTimeout(1500);
    await chat.keyboard.press('Escape');
    await chat.waitForTimeout(300);
    await tap(chat, chat.getByRole('button', {name: 'Open file: README.md', exact: true}).first());
    await chat.getByRole('tab', {name: 'Diff', exact: true}).waitFor();
    await tap(chat, chat.getByRole('tab', {name: 'Diff', exact: true}));
    await chat.waitForTimeout(1800);
    await chat.keyboard.press('Escape');
    await chat.waitForTimeout(300);
  });

  // A phone: the hub's agent that waits for an approval, allowed, and back to the list.
  const phonePage = await newPage(phoneBrowser, phoneOptions, [[machinesScript, hubMachines]]);
  await signIn(phonePage);
  await recordScene(phonePage, scene('phone'), frames, async () => {
    await phonePage.waitForTimeout(800);
    await tap(phonePage, phonePage.getByRole('list', {name: 'Instances'}).getByText('checkout-api'));
    const allow = phonePage.frameLocator('iframe:not([inert])').getByRole('button', {name: 'Allow'}).first();
    await allow.waitFor();
    await phonePage.waitForTimeout(1000);
    await tap(phonePage, allow);
    await phonePage.waitForTimeout(1600);
    await tap(phonePage, phonePage.frameLocator('iframe:not([inert])').getByRole('button', {name: 'Back to Agents'}));
    await phonePage.getByRole('list', {name: 'Instances'}).getByText('checkout-api').waitFor();
    await phonePage.waitForTimeout(1400);
  });
  await phonePage.context().close();

  // The queue: a task waits while a long turn works, another steers into it, then Stop.
  await recordScene(chat, scene('queue'), frames, async () => {
    await sendTask('Take the long route: refactor the layout step by step.');
    await chat.getByText('still working', {exact: false}).first().waitFor();
    await sendTask('Then add a dark theme to `styles.css`.');
    await chat.waitForTimeout(600);
    await tap(chat, box);
    await typeSlowly(chat, 'Keep the header sticky.');
    await tap(chat, chat.getByRole('button', {name: 'More ways to send'}));
    await chat.waitForTimeout(400);
    await tap(chat, chat.getByRole('menuitem', {name: /^Steer/}));
    await chat.getByText('Steered: Keep the header sticky', {exact: false}).waitFor();
    await chat.waitForTimeout(1500);
    const stop = chat.getByRole('button', {name: 'Stop agent'});
    if (await stop.isVisible()) await tap(chat, stop);
    await chat.waitForTimeout(1000);
  });
  await chat.context().close();
  if (errors.length) throw new Error(errors.join('\n'));

  const {agentapi_version: version} = await demo.status();
  const files = [];
  for (const language of Object.keys(languages)) files.push(await compose(frames, desktopBrowser, language));
  const seconds = scenes.reduce((sum, entry) => sum + entry.seconds, 0);
  await writeFile(path.join(output, 'capture.json'), JSON.stringify({
    captured_at: new Date().toISOString(),
    agentapi_version: version,
    agents: ['native-demo posing as Claude Code', 'nine native-demo instances behind agentapi hub serve, posing as Claude Code, Codex and Pi'],
    browser: desktopBrowser.version(),
    ffmpeg: execFileSync(ffmpeg, ['-version']).toString().split('\n')[0],
    size: {width, height, fps},
    seconds: Math.round(seconds * 10) / 10,
    scenes: scenes.map(entry => entry.id),
    videos: files.map(file => path.basename(file)),
    posters: Object.values(languages).map(entry => entry.poster),
  }, null, 2) + '\n');
  console.log(`Recorded ${seconds.toFixed(1)} s of AgentAPI ${version} with no browser errors.`);
} catch (error) {
  for (const server of servers) {
    if (server.log()) console.error(`--- ${server.name} log ---\n${server.log()}`);
  }
  throw error;
} finally {
  for (const browser of browsers) await browser.close();
  for (const server of servers) {
    server.child.kill('SIGTERM');
    const timer = setTimeout(() => server.child.kill('SIGKILL'), 8000);
    await server.exited;
    clearTimeout(timer);
  }
  if (!process.env.AGENTAPI_VIDEO_KEEP) await rm(root, {recursive: true, force: true});
  else console.log(`Kept the frames in ${root}`);
}
