/**
 * Captures the documentation screenshots of the hub's Issues page (the issue dispatch), its GitHub
 * and Providers settings, and the chat's model menu with the hub's providers, in docs/screenshots/.
 * See docs/screenshots/README.md.
 *
 * The hub is a real `agentapi hub serve` on a scratch home directory: it serves the page, signs the
 * browser in, keeps the favorites and the model providers (put through its API, priced from an
 * OpenRouter list the script writes as the hub's copy), and reads its GitHub App from a
 * github-app.json the script writes (a key made here, installed on `acme` and on `dave`). What would
 * need machines, agents and GitHub is fixed data, fed to the page:
 *
 * - the hub's stream: the page's EventSource gets the script's instances (the list's rows: job
 *   agents named after their issues, and two others) in place of the hub's, and its board of the
 *   dispatch (three machines, one offline, and the jobs of acme/web, acme/api and acme/infra);
 * - the calls that go to GitHub (`/api/github/repos`, `/api/github/issues`) and the accounts'
 *   pictures are answered by Playwright routes; any other address off this machine is refused;
 * - the page's clock is fixed (today at 14:30 UTC, or AGENTAPI_CAPTURE_TIME), so ages and times of
 *   day are the same from run to run.
 *
 * For the model menu, an `agentapi server` joins the hub and runs a small Claude Code stand-in the
 * script writes (it answers the control protocol with Claude Code's models), so the menu lists the
 * hub's providers. For Check connection, a fake OpenAI-compatible endpoint lists six models.
 *
 * Environment: AGENTAPI_PLAYWRIGHT_MODULE (playwright-core's index.mjs), AGENTAPI_BINARY (default
 * out/agentapi), AGENTAPI_DISPATCH_PORT (the first of three free ports, default 3400),
 * CHROMIUM_PATH (optional), AGENTAPI_CAPTURE_TIME (optional, an ISO time for the page's clock).
 */
import path from 'node:path';
import os from 'node:os';
import net from 'node:net';
import http from 'node:http';
import {spawn, execFileSync} from 'node:child_process';
import {generateKeyPairSync} from 'node:crypto';
import {mkdtemp, mkdir, writeFile, rm, chmod} from 'node:fs/promises';
import {fileURLToPath, pathToFileURL} from 'node:url';

const moduleName = process.env.AGENTAPI_PLAYWRIGHT_MODULE;
if (!moduleName) {
  console.error('Set AGENTAPI_PLAYWRIGHT_MODULE to the path of playwright-core\'s index.mjs; see docs/screenshots/README.md.');
  process.exit(1);
}
const {chromium} = await import(path.isAbsolute(moduleName) ? pathToFileURL(moduleName).href : moduleName);
const repo = fileURLToPath(new URL('..', import.meta.url));
const binary = path.resolve(process.env.AGENTAPI_BINARY || path.join(repo, 'out', 'agentapi'));
const port = Number(process.env.AGENTAPI_DISPATCH_PORT || 3400);
const hubPort = port;
const providerPort = port + 1;
const chatPort = port + 2;
const output = path.join(repo, 'docs', 'screenshots');
const images = [];

// The page's clock: today at 14:30 UTC, unless set. The times of day the page shows stay the same
// from run to run; the hub's own clock (its copy of OpenRouter's prices) stays within a day of it.
const now = process.env.AGENTAPI_CAPTURE_TIME
  ? new Date(process.env.AGENTAPI_CAPTURE_TIME)
  : new Date(`${new Date().toISOString().slice(0, 10)}T14:30:00Z`);
if (Number.isNaN(now.getTime())) throw new Error('AGENTAPI_CAPTURE_TIME is not a time');
// at is the time minutes before the page's now.
const at = minutes => new Date(now.getTime() - minutes * 60_000).toISOString();
const hours = h => h * 60;

const hubToken = 'docs-token';
const hubSecret = 'docs-secret';

// ---------------------------------------------------------------------------------------------
// GitHub: the repositories the app is on, the latest pushed first, and their open issues.

const acmeRepos = ['web', 'api', 'infra', 'design-system', 'mobile', 'billing', 'search', 'auth', 'docs', 'analytics',
  'payments', 'notifications', 'gateway', 'cli', 'terraform', 'helm-charts', 'status-page', 'email-templates',
  'feature-flags', 'load-tests', 'onboarding', 'admin', 'sdk-js', 'sdk-go'];
const daveRepos = ['notes', 'dotfiles', 'homelab'];
const githubRepos = [
  ...acmeRepos.slice(0, 18).map(name => `acme/${name}`),
  'dave/notes',
  ...acmeRepos.slice(18).map(name => `acme/${name}`),
  'dave/dotfiles', 'dave/homelab',
].map((fullName, i) => ({
  full_name: fullName,
  html_url: `https://github.com/${fullName}`,
  default_branch: 'main',
  private: !fullName.startsWith('dave/'),
  pushed_at: at(40 + i * 170),
  updated_at: at(40 + i * 170),
}));

const issue = (repoName, number, title, labels, body) => ({
  number, title, body, state: 'open',
  html_url: `https://github.com/${repoName}/issues/${number}`,
  labels: labels.map(name => ({name})),
  user: {login: 'dave'},
});
const githubIssues = {
  'acme/web': [
    issue('acme/web', 23, 'Flaky e2e: checkout times out on CI', ['bug', 'ci'], 'The checkout spec times out about one run in five on CI, never locally.'),
    issue('acme/web', 22, 'Add a keyboard shortcut to toggle the sidebar', ['enhancement'], 'Something like ⌘\\, as in the editor.'),
    issue('acme/web', 18, 'Dark mode: chart tooltips are unreadable', ['bug'], 'Grey on grey in the dark theme; the text needs a contrast of 4.5:1.'),
    issue('acme/web', 15, 'Settings: autosave drafts while typing', ['enhancement'], 'Drafts of the settings are lost when the tab closes. Save them as the user types.'),
    issue('acme/web', 12, '登入後重新導向會無限循環', ['bug'], '登入後如果網址帶著 ?next=/login，會一直重新導向回登入頁。'),
  ],
  'acme/api': [
    issue('acme/api', 11, 'Rate-limit the webhook endpoint', ['security'], 'POST /webhooks takes any number of calls; limit it per source.'),
    issue('acme/api', 9, '升級到 Go 1.24', ['chore'], '升級 go.mod 與 CI 的 Go 版本，處理新的 vet 警告。'),
    issue('acme/api', 7, 'Paginate /v1/orders and return the total', ['enhancement'], 'Add page and per_page, and the total count in the answer.'),
  ],
  'acme/infra': [
    issue('acme/infra', 5, 'Nightly backup job writes to the wrong bucket', ['bug'], 'The cron job uses the staging bucket since the last change to its env.'),
  ],
};

// The hub's GitHub App: registered for the acme organization, installed on acme (selected
// repositories) and on dave's account (all of them).
const githubApp = () => {
  const {privateKey} = generateKeyPairSync('rsa', {modulusLength: 2048});
  return {
    app_id: 1048576, slug: 'acme-agents', client_id: 'Iv23liDocsClientID00', client_secret: 'docs-client-secret',
    pem: privateKey.export({type: 'pkcs1', format: 'pem'}), webhook_secret: 'docs-webhook-secret', bot_user_id: 208_000_123,
    owner: 'acme', owner_type: 'Organization', hub_url: `http://127.0.0.1:${hubPort}`,
    installations: [
      {id: 61000001, account: 'acme', account_type: 'Organization', repository_selection: 'selected'},
      {id: 61000002, account: 'dave', account_type: 'User', repository_selection: 'all'},
    ],
  };
};

// The accounts' pictures, drawn here in place of GitHub's: an identicon of a 5 × 5 grid, mirrored.
const identicon = (seed, color) => {
  let h = 2166136261;
  for (const c of seed) h = Math.imul(h ^ c.charCodeAt(0), 16777619) >>> 0;
  const cells = [];
  for (let y = 0; y < 5; y++) {
    for (let x = 0; x < 3; x++) {
      h = Math.imul(h ^ (h >>> 13), 0x5bd1e995) >>> 0;
      if (h % 2 === 0) {
        cells.push([x, y]);
        if (x < 2) cells.push([4 - x, y]);
      }
    }
  }
  const rects = cells.map(([x, y]) => `<rect x="${12 + x * 14.4}" y="${12 + y * 14.4}" width="14.4" height="14.4"/>`).join('');
  return `<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96"><rect width="96" height="96" fill="#f0f0f0"/><g fill="${color}">${rects}</g></svg>`;
};
const avatars = {acme: identicon('acme', '#d4a72c'), dave: identicon('dave', '#5a8fd8')};

// ---------------------------------------------------------------------------------------------
// The dispatch: the machines whose launchers report to the hub, and the jobs.

const cost = (usd, workMinutes, [input, output, cacheWrite, cacheRead], calls, tasks = 1) => ({
  usd, work_seconds: workMinutes * 60, calls, tasks,
  tokens: {input, output, cache_write: cacheWrite, cache_read: cacheRead},
});
const instanceOf = (host, key) => `${host}~${key.replace('/', '~').replace('#', '~')}`;
const run = (host, key, agent, startedAt, endedAt, endReason, spent, serverID) => ({
  round: 1, agent, host, instance_id: instanceOf(host, key), server_id: serverID, started_at: startedAt,
  ...(endedAt ? {ended_at: endedAt, end_reason: endReason} : {}),
  cost: spent, events: spent,
});
const job = ({key, title, state, agent, host, created, updated, runs, pr, error, messages}) => {
  const [repoName, number] = key.split('#');
  return {
    key, repo: repoName, issue: Number(number), title, host, agent, instance_id: instanceOf(host, key), state,
    ...(error ? {error} : {}),
    issue_url: `https://github.com/${repoName}/issues/${number}`,
    ...(pr ? {pr_url: `https://github.com/${repoName}/pull/${pr}`} : {}),
    default_branch: 'main', credentials: true, created_at: created, updated_at: updated, runs, round: 1,
    ...(messages ? {messages} : {}),
  };
};

const jobs = [
  job({key: 'acme/web#15', title: 'Settings: autosave drafts while typing', state: 'starting', agent: 'pi', host: 'pve-dev',
    created: at(140), updated: at(0.3),
    runs: [
      run('pve-dev', 'acme/web#15', 'claude', at(140), at(64), 'replaced', cost(2.12, 65, [3_100, 31_000, 88_000, 2_010_000], 71), '5e1c0a9d'),
      run('pve-dev', 'acme/web#15', 'codex', at(64), at(0.3), 'replaced', cost(1.07, 42, [96_000, 52_000, 0, 1_140_000], 64), '8b27f3c1'),
      run('pve-dev', 'acme/web#15', 'pi', at(0.3), '', '', cost(0, 0, [0, 0, 0, 0], 0, 0), 'c4d9e270'),
    ],
    messages: [{at: at(90), source: 'comment', author: 'dave', agent: 'claude', text: 'Debounce at 800 ms, and show "Saved" in the header instead of a toast.'}]}),
  job({key: 'acme/web#18', title: 'Dark mode: chart tooltips are unreadable', state: 'working', agent: 'codex', host: 'mac-mini',
    created: at(12), updated: at(1),
    runs: [run('mac-mini', 'acme/web#18', 'codex', at(12), '', '', cost(0.42, 11, [41_000, 21_000, 0, 388_000], 29), '91aa04e6')]}),
  job({key: 'acme/api#7', title: 'Paginate /v1/orders and return the total', state: 'working', agent: 'claude', host: 'pve-dev',
    created: at(23), updated: at(2),
    runs: [run('pve-dev', 'acme/api#7', 'claude', at(23), '', '', cost(0.97, 19, [2_100, 12_000, 25_000, 1_000_000], 38), '2fd8b613')]}),
  job({key: 'acme/web#12', title: '登入後重新導向會無限循環', state: 'review', agent: 'claude', host: 'pve-dev', pr: 21,
    created: at(52), updated: at(6),
    runs: [run('pve-dev', 'acme/web#12', 'claude', at(52), '', '', cost(1.84, 41, [3_400, 23_000, 54_000, 1_920_000], 63, 3), '7c03e5ab')],
    messages: [
      {at: at(30), source: 'hub', agent: 'claude', text: 'Keep the ?next= parameter when it points inside the app; drop it otherwise.'},
      {at: at(14), source: 'review', author: 'dave', review_state: 'changes_requested', comments: 2, agent: 'claude',
        text: 'Add a test for the loop itself, not only the redirect target.'},
    ]}),
  job({key: 'acme/api#9', title: '升級到 Go 1.24', state: 'failed', agent: 'codex', host: 'pve-dev',
    created: at(66), updated: at(31),
    error: 'its AgentAPI exited 6 times in 30m0s; see /home/dev/.agentapi/jobs/acme~api~9/agentapi.log',
    runs: [run('pve-dev', 'acme/api#9', 'codex', at(66), at(31), 'failed', cost(0.18, 4, [38_000, 11_000, 0, 150_000], 17), 'e60b5d28')]}),
  job({key: 'acme/infra#3', title: 'Rotate the staging TLS certificates', state: 'stopped', agent: 'claude', host: 'gpu-box',
    created: at(hours(5)), updated: at(hours(2)),
    runs: [run('gpu-box', 'acme/infra#3', 'claude', at(hours(5)), at(hours(2)), 'stopped', cost(0.63, 14, [1_800, 9_000, 31_000, 560_000], 24), '3a9e61f0')]}),
  job({key: 'acme/web#10', title: 'Footer links point to the old docs site', state: 'done', agent: 'codex', host: 'pve-dev', pr: 19,
    created: at(hours(22.4)), updated: at(hours(21.2)),
    runs: [run('pve-dev', 'acme/web#10', 'codex', at(hours(22.4)), at(hours(21.2)), 'done', cost(0.55, 13, [58_000, 26_000, 0, 610_000], 31), 'b81f4c07')]}),
  job({key: 'acme/api#4', title: 'Return 404 for an unknown order ID', state: 'done', agent: 'claude', host: 'pve-dev', pr: 17,
    created: at(hours(46)), updated: at(hours(45.1)),
    runs: [run('pve-dev', 'acme/api#4', 'claude', at(hours(46)), at(hours(45.1)), 'done', cost(0.48, 12, [1_500, 8_000, 22_000, 470_000], 22), '06d7a3e9')]}),
];

// What a machine's launcher reports of a job it has.
const launcherJob = (key, run, state, home) => {
  const found = jobs.find(entry => entry.key === key);
  const number = found.issue;
  const name = found.repo.split('/')[1];
  const last = found.runs.at(-1);
  return {
    key, agent: found.agent, run, port: 4100 + number, instance_id: found.instance_id, server_id: last.server_id,
    branch: `agent/issue-${number}`, default_branch: 'main', worktree: `${home}/src/${name}.worktrees/${number}`,
    state, ...(found.error ? {error: found.error} : {}), credentials: 'hub', started_at: last.started_at,
  };
};

// The board of the dispatch as the hub's stream sends it: the jobs and the machines. names renames
// the machines (the Assign menu's long names); withJobs false is a hub that dispatched nothing yet.
const makeBoard = ({names = {}, withJobs = true} = {}) => {
  const name = host => names[host] ?? host;
  const shownJobs = withJobs ? jobs : [];
  const machine = ({hostname, url, agents, defaultAgent, maxJobs, repos: hostRepos, clone, online, lastSeen, stopped, jobsOn, home}) => {
    const working = shownJobs.filter(entry => entry.host === hostname && !['done', 'stopped', 'failed'].includes(entry.state)).length;
    return {
      hostname: name(hostname), url, agentapi_version: '', repos: hostRepos, agents, default_agent: defaultAgent, max_jobs: maxJobs, clone,
      credentials_available: false, hub_credentials: true,
      jobs: withJobs ? jobsOn.map(([key, runNumber, state]) => ({...launcherJob(key, runNumber, state, home)})) : [],
      heartbeat_seconds: 60, last_seen: lastSeen, ...(stopped ? {stopped: true} : {}), online, active: working,
    };
  };
  const hosts = [
    machine({hostname: 'gpu-box', url: 'http://10.0.0.23:7300', agents: ['claude'], defaultAgent: 'claude', maxJobs: 2,
      repos: ['acme/api', 'acme/infra'], clone: true, online: false, lastSeen: at(hours(3)), stopped: true, jobsOn: [], home: '/home/dev'}),
    machine({hostname: 'mac-mini', url: 'http://10.0.0.22:7300', agents: ['codex', 'claude'], defaultAgent: 'codex', maxJobs: 2,
      repos: ['acme/web'], clone: false, online: true, lastSeen: at(0.2), jobsOn: [['acme/web#18', 1, 'running']], home: '/Users/dev'}),
    machine({hostname: 'pve-dev', url: 'http://10.0.0.21:7300', agents: ['claude', 'codex', 'pi'], defaultAgent: 'claude', maxJobs: 4,
      repos: ['acme/api', 'acme/infra', 'acme/web'], clone: true, online: true, lastSeen: at(0.4),
      jobsOn: [['acme/api#7', 1, 'running'], ['acme/api#9', 1, 'failed'], ['acme/web#12', 1, 'running'], ['acme/web#15', 3, 'starting']], home: '/home/dev'}),
  ].sort((a, b) => a.hostname.localeCompare(b.hostname));
  const renamedJobs = shownJobs.map(entry => ({...entry, host: name(entry.host), runs: entry.runs.map(r => ({...r, host: name(r.host)}))}));
  return {jobs: [...renamedJobs].sort((a, b) => Date.parse(b.updated_at) - Date.parse(a.updated_at)), hosts};
};

// ---------------------------------------------------------------------------------------------
// The instances the hub follows, as its stream sends them: the jobs' AgentAPIs, named after their
// issues as the launcher names them, and two agents of no job.

const tokens = (input, output, cacheWrite, cacheRead) => ({input, output, cache_write: cacheWrite, cache_read: cacheRead, total: input + output + cacheWrite + cacheRead});
const spend = (scope, dayUSD, weekUSD, providers) => ({
  scope, day_usd: dayUSD, week_usd: weekUSD, updated_at: at(1),
  day_tokens: tokens(4_000, 30_000, 60_000, Math.round(dayUSD * 900_000)),
  week_tokens: tokens(15_000, 120_000, 240_000, Math.round(weekUSD * 900_000)),
  ...(providers ? {providers} : {}),
});
const summary = (instance, fields) => ({
  instance: {id: instance.id, server_id: instance.server_id, agent_type: instance.agent_type, transport: 'native', hostname: instance.hostname, name: instance.name},
  status: 'stable', lifecycle: 'ready', attention: 'none', title: '', pending_requests: 0, queued_messages: 0, tasks: 1, unread: false,
  seen_at: at(1), ...fields,
});
const hubInstance = ({id, name, hostname, agentType, serverID, port: instancePort, group, state, connection = 'connected', work, summary: fields}) => {
  const instance = {
    id, name, hostname, agent_type: agentType, server_id: serverID, transport: 'native', source: 'webhook',
    url: `http://${hostname}.lan:${instancePort}`, port: instancePort, last_seen: at(0.1), changed_at: at(1),
    connection, state, ...(group ? {group} : {}), ...(work ? {work} : {}), heartbeat_seconds: 30,
  };
  if (fields) instance.summary = summary(instance, fields);
  return instance;
};
const workTime = (dayMinutes, weekMinutes, since) => ({last_24h_seconds: dayMinutes * 60, last_7d_seconds: weekMinutes * 60, ...(since ? {working_since: since} : {})});
const instances = [
  hubInstance({id: instanceOf('pve-dev', 'acme/api#7'), name: 'acme/api#7', hostname: 'pve-dev', agentType: 'claude', serverID: '2fd8b613',
    port: 4107, group: 'Issues', state: 'needs-you',
    summary: {status: 'running', attention: 'request', pending_requests: 1, title: 'Paginate /v1/orders',
      request: {id: 'req-7', kind: 'approval', title: 'go test ./... -race -count=1', created_at: at(2)},
      task: {id: 1, prompt: '', started_at: at(23), status: 'running'}, spend: spend('pve-dev:claude:api.worktrees/7', 0.97, 0.97)}}),
  hubInstance({id: instanceOf('mac-mini', 'acme/web#18'), name: 'acme/web#18', hostname: 'mac-mini', agentType: 'codex', serverID: '91aa04e6',
    port: 4118, group: 'Issues', state: 'working', work: workTime(11, 11, at(12)),
    summary: {status: 'running', title: 'Raising tooltip contrast to 4.5:1 in the dark chart theme',
      task: {id: 1, prompt: '', started_at: at(12), status: 'running'}, spend: spend('mac-mini:codex:web.worktrees/18', 0.42, 0.42)}}),
  hubInstance({id: instanceOf('pve-dev', 'acme/web#12'), name: 'acme/web#12', hostname: 'pve-dev', agentType: 'claude', serverID: '7c03e5ab',
    port: 4112, group: 'Issues', state: 'unread',
    summary: {unread: true, tasks: 3, title: 'Added the loop test and pushed it to #21',
      task: {id: 3, prompt: '', started_at: at(14), ended_at: at(6), status: 'completed'}, spend: spend('pve-dev:claude:web.worktrees/12', 1.84, 1.84)}}),
  hubInstance({id: instanceOf('pve-dev', 'acme/web#15'), name: 'acme/web#15', hostname: 'pve-dev', agentType: 'pi', serverID: 'c4d9e270',
    port: 4115, group: 'Issues', state: 'connecting', connection: 'connecting'}),
  hubInstance({id: 'pve-dev:3284', name: 'agentapi', hostname: 'pve-dev', agentType: 'codex', serverID: 'f2a7c9e4', port: 3284,
    state: 'working', work: workTime(54, 410, at(4)),
    summary: {status: 'running', tasks: 12, title: 'Profile the hub\'s SSE fan-out under 200 browsers',
      task: {id: 12, prompt: '', started_at: at(4), status: 'running'},
      spend: spend('pve-dev:codex:agentapi', 2.14, 9.8, [{provider: 'gateway', day_usd: 2.14, week_usd: 9.8,
        day_tokens: tokens(410_000, 96_000, 0, 2_600_000), week_tokens: tokens(1_900_000, 430_000, 0, 11_800_000)}]),
      provider_health: [{provider: 'gateway', requests: 118, failures: 2, client_errors: 0, first_byte_p50_ms: 840, first_byte_p95_ms: 2_300,
        last_success_at: at(0.5), last_failure: {at: at(14), kind: 'status', status: 529, message: 'Overloaded'}}]}}),
  hubInstance({id: 'mac-mini:3284', name: 'notes', hostname: 'mac-mini', agentType: 'claude', serverID: '0be43d18', port: 3284,
    state: 'idle',
    summary: {tasks: 4, title: 'Weekly review outline',
      task: {id: 4, prompt: '', started_at: at(51), ended_at: at(25), status: 'completed'},
      spend: spend('mac-mini:claude:notes', 0.31, 1.12, [{provider: 'local', day_usd: 0.31, week_usd: 1.12,
        day_tokens: tokens(820_000, 140_000, 0, 0), week_tokens: tokens(2_900_000, 520_000, 0, 0)}]),
      provider_health: [{provider: 'local', requests: 36, failures: 0, client_errors: 0, first_byte_p50_ms: 210, first_byte_p95_ms: 480,
        last_success_at: at(26)}]}}),
];
const makeInstances = (names = {}) => instances.map(instance => {
  const hostname = names[instance.hostname] ?? instance.hostname;
  return {...instance, hostname, ...(instance.summary ? {summary: {...instance.summary, instance: {...instance.summary.instance, hostname}}} : {})};
});

// The page's EventSource, with the script's instances and board in place of the hub's. The hub's
// version (the instances event) and favorites come through.
const streamScript = ({instances: fixedInstances, board}) => {
  window.EventSource = class extends window.EventSource {
    addEventListener(type, listener, options) {
      if (type === 'instance' || type === 'removed') return super.addEventListener(type, () => {}, options);
      if (type !== 'instances' && type !== 'dispatch') return super.addEventListener(type, listener, options);
      return super.addEventListener(type, event => {
        const data = JSON.parse(event.data);
        let replaced;
        if (type === 'instances') replaced = {...data, instances: fixedInstances.map(instance => ({...instance, agentapi_version: data.hub_version}))};
        else replaced = {...board, hosts: board.hosts.map(host => ({...host, agentapi_version: window.__hubVersion || ''}))};
        if (type === 'instances') window.__hubVersion = data.hub_version;
        listener.call(this, new MessageEvent(event.type, {data: JSON.stringify(replaced), lastEventId: event.lastEventId}));
      }, options);
    }
  };
};

// ---------------------------------------------------------------------------------------------
// The model providers the hub keeps, and its copy of OpenRouter's list.

const providers = [
  {id: 'gateway', name: 'LiteLLM gateway', base_url: 'https://gateway.example.com/v1', api_key: 'sk-docs-gateway-key',
    models: [{id: 'Kimi-K3'}, {id: 'GLM-5'}, {id: 'Qwen3-Coder'}]},
  {id: 'local', name: 'Local vLLM', base_url: 'http://10.0.0.12:8000/v1', models: [{id: 'gpt-oss-120b'}]},
];
const price = (input, output, cacheRead = 0, cacheWrite = 0) => ({input, output, cache_read: cacheRead, cache_write: cacheWrite});
const openRouterModels = [
  {id: 'moonshotai/kimi-k3', name: 'MoonshotAI: Kimi K3', price: price(0.6, 2.5, 0.15)},
  {id: 'z-ai/glm-5', name: 'Z.ai: GLM 5', price: price(0.6, 2.2, 0.11)},
  {id: 'qwen/qwen3-coder', name: 'Qwen: Qwen3 Coder 480B A35B', price: price(0.22, 0.95)},
  {id: 'openai/gpt-oss-120b', name: 'OpenAI: gpt-oss-120b', price: price(0.05, 0.25)},
  {id: 'deepseek/deepseek-v4', name: 'DeepSeek: DeepSeek V4', price: price(0.3, 1.2, 0.03)},
  {id: 'meta-llama/llama-5-70b', name: 'Meta: Llama 5 70B', price: price(0.12, 0.4)},
  {id: 'anthropic/claude-sonnet-5.5', name: 'Anthropic: Claude Sonnet 5.5', price: price(3, 15, 0.3, 3.75)},
];
// Got three hours before the page's now, or an hour ago if that is further from the hub's clock than a
// day: the hub gets the list again only when its copy is a day old.
const pricesFetched = () => {
  const wanted = now.getTime() - 3 * 3600_000;
  return new Date(Date.now() - wanted > 23 * 3600_000 ? Date.now() - 3600_000 : wanted).toISOString();
};

// The fake provider for Check connection: an OpenAI-compatible endpoint that lists six models.
const providerModels = ['Kimi-K3', 'GLM-5', 'Qwen3-Coder', 'DeepSeek-V4', 'gpt-oss-120b', 'Llama-5-70B'];
const startFakeProvider = () => new Promise((resolve, reject) => {
  const server = http.createServer((request, response) => {
    if (request.url === '/v1/models') {
      setTimeout(() => {
        response.writeHead(200, {'Content-Type': 'application/json'});
        response.end(JSON.stringify({object: 'list', data: providerModels.map(id => ({id, object: 'model', owned_by: 'lab'}))}));
      }, 175);
      return;
    }
    response.writeHead(404);
    response.end();
  });
  server.once('error', reject);
  server.listen(providerPort, '127.0.0.1', () => resolve(server));
});

// A stand-in for Claude Code: it reports a login, and in the stream-json mode answers AgentAPI's
// control requests with Claude Code's models, as `claude` does; it never runs a turn.
const claudeModels = [
  {value: 'default', resolvedModel: 'claude-opus-5-5', displayName: 'Default (recommended)', description: 'Opus 5.5 · Best for everyday, complex tasks'},
  {value: 'opus', resolvedModel: 'claude-opus-5-5', displayName: 'Opus', description: 'Opus 5.5 · Best for everyday, complex tasks'},
  {value: 'fable', resolvedModel: 'claude-fable-5-1', displayName: 'Fable', description: 'Fable 5.1 · Most capable for your hardest and longest-running tasks'},
  {value: 'sonnet', resolvedModel: 'claude-sonnet-5-5', displayName: 'Sonnet', description: 'Sonnet 5.5 · Efficient for routine tasks'},
  {value: 'haiku', resolvedModel: 'claude-haiku-5-5', displayName: 'Haiku', description: 'Haiku 5.5 · Fastest for quick answers', supportsEffort: false},
].map(model => ({supportsEffort: true, supportedEffortLevels: ['low', 'medium', 'high', 'xhigh', 'max'], ...model}));
const fakeClaude = `#!/usr/bin/env node
'use strict';
const args = process.argv.slice(2);
if (args[0] === 'auth' && args[1] === 'status') {
  console.log(JSON.stringify({loggedIn: true, authMethod: 'claude.ai'}));
  process.exit(0);
}
if (args.length === 1 && (args[0] === '--version' || args[0] === '-v')) {
  console.log('2.1.299 (Claude Code)');
  process.exit(0);
}
const models = ${JSON.stringify(claudeModels)};
const send = frame => process.stdout.write(JSON.stringify(frame) + '\\n');
const lines = require('readline').createInterface({input: process.stdin});
lines.on('line', line => {
  let message;
  try { message = JSON.parse(line); } catch { return; }
  if (message.type !== 'control_request') return;
  const request = message.request || {};
  let response;
  switch (request.subtype) {
    case 'initialize':
      response = {claude_code_version: '2.1.299', current_permission_mode: 'bypassPermissions', session_state: 'idle', models};
      break;
    case 'get_settings':
      response = {effective: {}, applied: {model: 'claude-opus-5-5', effort: 'medium'}};
      break;
    case 'interrupt': case 'set_permission_mode': case 'set_model': case 'set_max_thinking_tokens': case 'apply_flag_settings':
      response = {};
      break;
    default:
      send({type: 'control_response', response: {subtype: 'error', request_id: message.request_id, error: 'not answered here: ' + request.subtype}});
      return;
  }
  send({type: 'control_response', response: {subtype: 'success', request_id: message.request_id, response}});
});
lines.on('close', () => process.exit(0));
`;

// ---------------------------------------------------------------------------------------------
// Processes, waiting and screenshots, as capture-docs.mjs has them.

const portFree = p => new Promise(resolve => {
  const socket = net.connect({host: '127.0.0.1', port: p});
  socket.once('connect', () => { socket.destroy(); resolve(false); });
  socket.once('error', () => resolve(true));
});
const servers = [];
const startProcess = (name, cwd, env, args, program = binary) => {
  const child = spawn(program, args, {cwd, env, stdio: ['ignore', 'pipe', 'pipe']});
  let log = '';
  const keep = chunk => { log = (log + chunk).slice(-6000); };
  child.stdout.on('data', keep);
  child.stderr.on('data', keep);
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
const hubAPI = `http://127.0.0.1:${hubPort}`;
const hubCall = async (route, method = 'GET', body) => {
  const response = await fetch(`${hubAPI}${route}`, {method,
    headers: {Authorization: `Bearer ${hubToken}`, ...(body === undefined ? {} : {'Content-Type': 'application/json'})},
    body: body === undefined ? undefined : JSON.stringify(body)});
  if (!response.ok) throw new Error(`${method} ${route}: ${response.status} ${await response.text()}`);
  const text = await response.text();
  return text ? JSON.parse(text) : undefined;
};

const screenshotStyle = 'nextjs-portal, [data-sonner-toaster], [data-toasts-clear] { visibility: hidden !important; }';
const errors = [];
const save = async (page, target, name, options = {}) => {
  // A toast is hidden in the image, but one is a sign something failed.
  const toasts = await page.locator('[data-sonner-toast]').allInnerTexts();
  if (toasts.length) errors.push(`${name}: a toast showed: ${toasts.join(' | ')}`);
  await target.screenshot({path: path.join(output, name), animations: 'disabled', style: screenshotStyle, ...options});
  images.push(name);
  console.log(`Captured ${name}`);
};
const settle = async page => { await page.mouse.move(0, 0); await page.waitForTimeout(400); };
const shot = async (page, name) => { await settle(page); await save(page, page, name); };
const elementShot = async (page, locator, name) => { await settle(page); await save(page, locator, name); };
const regionShot = async (page, name, locators, pad = 16) => {
  await settle(page);
  const boxes = await Promise.all(locators.map(locator => locator.boundingBox()));
  const viewport = page.viewportSize();
  const x = Math.max(0, Math.min(...boxes.map(b => b.x)) - pad);
  const y = Math.max(0, Math.min(...boxes.map(b => b.y)) - pad);
  const right = Math.min(viewport.width, Math.max(...boxes.map(b => b.x + b.width)) + pad);
  let bottom = Math.min(viewport.height, Math.max(...boxes.map(b => b.y + b.height)) + pad);
  if (viewport.height - bottom < 48) bottom = viewport.height;
  await save(page, page, name, {clip: {x, y, width: right - x, height: bottom - y}});
};
const panelShot = async (page, locator, name) => elementShot(page, locator, name);
const clipShot = async (page, name, clip) => { await settle(page); await save(page, page, name, {clip}); };

const contextOptions = {deviceScaleFactor: 1, colorScheme: 'light', locale: 'en-US', timezoneId: 'UTC', reducedMotion: 'reduce'};
const desktop = {viewport: {width: 1440, height: 1000}};
const phone = {viewport: {width: 390, height: 844}, isMobile: true, hasTouch: true};
const isLocal = url => ['127.0.0.1', 'localhost'].includes(url.hostname);

// openHub signs in to the hub in a new browser context whose stream, GitHub calls and clock are
// the script's. github false is a hub not connected to GitHub yet.
const openHub = async (browser, options, {names = {}, withJobs = true, github = true} = {}) => {
  const context = await browser.newContext({...contextOptions, ...options});
  context.on('weberror', event => errors.push(event.error().message));
  await context.clock.setFixedTime(now);
  await context.addInitScript(() => { try { localStorage.setItem('theme', 'light'); } catch {} });
  await context.addInitScript(streamScript, {instances: makeInstances(names), board: makeBoard({names, withJobs})});
  // Nothing leaves this machine: the routes below answer what would go to GitHub.
  await context.route(url => !isLocal(url), route => route.abort());
  await context.route(/^https:\/\/github\.com\/[^/]+\.png/, route => {
    const account = new URL(route.request().url()).pathname.slice(1).replace(/\.png$/, '');
    return route.fulfill({contentType: 'image/svg+xml', body: avatars[account] ?? identicon(account, '#888')});
  });
  await context.route(`${hubAPI}/api/github/repos`, route => route.fulfill({json: {repos: githubRepos}}));
  await context.route(`${hubAPI}/api/github/issues?*`, route => {
    if (route.request().method() !== 'GET') return route.fallback();
    const repoName = new URL(route.request().url()).searchParams.get('repo');
    return route.fulfill({json: {issues: githubIssues[repoName] ?? []}});
  });
  if (!github) await context.route(`${hubAPI}/api/github`, route => route.fulfill({json: {connected: false, installations: []}}));
  const page = await context.newPage();
  page.setDefaultTimeout(20_000);
  await page.goto(`${hubAPI}/`);
  await page.evaluate(() => document.fonts.ready);
  await page.getByLabel('Hub token').fill(hubToken);
  await page.getByRole('button', {name: 'Sign in'}).click();
  await page.getByRole('list', {name: 'Instances'}).getByText('acme/api#7').first().waitFor();
  return page;
};

// openIssues opens the Issues page and waits for its board: the cards of every section.
const openIssues = async (page, {connected = true} = {}) => {
  await page.getByRole('button', {name: /^Issues(, \d+ needs? you)?$/}).click();
  if (connected) {
    await page.getByRole('list', {name: 'To do'}).getByText('Rate-limit the webhook endpoint').waitFor();
    await page.getByRole('list', {name: 'Needs you'}).getByText('Paginate /v1/orders and return the total').waitFor();
  } else {
    await page.getByRole('heading', {name: 'Hand issues to your agents'}).waitFor();
  }
  await page.waitForTimeout(600);
};
const openCard = async (page, title) => {
  await page.locator('[data-issues-view]').getByText(title, {exact: true}).first().click();
  const detail = page.locator('[data-issue-detail]');
  await detail.getByRole('heading', {name: title}).waitFor();
  await page.waitForTimeout(500);
  return detail;
};

const root = await mkdtemp(path.join(os.tmpdir(), 'agentapi-dispatch-docs-'));
let browser;
let fakeProvider;
try {
  for (let p = port; p < port + 3; p++) {
    if (!await portFree(p)) throw new Error(`Port ${p} is in use; set AGENTAPI_DISPATCH_PORT to the first of three free ports.`);
  }
  // A clean environment: a scratch home, none of the shell's AGENTAPI_* or CLAUDE_CODE_* variables,
  // and a proxy that goes nowhere for the hub, so it can't reach GitHub or OpenRouter by mistake
  // (Go doesn't proxy loopback addresses: Check connection still reaches the fake provider).
  const home = path.join(root, 'home');
  const hubDir = path.join(home, '.agentapi', 'hub');
  await mkdir(hubDir, {recursive: true});
  const env = {HOME: home, PATH: '/usr/local/bin:/usr/bin:/bin', SHELL: '/bin/bash', LANG: 'C.UTF-8', TERM: 'xterm-256color'};
  const hubEnv = {...env, HTTPS_PROXY: 'http://127.0.0.1:9', HTTP_PROXY: 'http://127.0.0.1:9', NO_PROXY: '127.0.0.1,localhost'};

  await writeFile(path.join(hubDir, 'github-app.json'), JSON.stringify(githubApp(), null, 2), {mode: 0o600});
  await writeFile(path.join(hubDir, 'openrouter.json'), JSON.stringify({version: 1, fetched_at: pricesFetched(), models: openRouterModels}), {mode: 0o600});
  startProcess('hub', root, hubEnv, ['hub', 'serve', '--bind', '127.0.0.1', '--port', String(hubPort),
    '--token', hubToken, '--secret', hubSecret, '--state', path.join(hubDir, 'instances.json')]);
  await poll(async () => (await fetch(`${hubAPI}/api/session`)).ok, 'the hub');
  const {revision} = await hubCall('/api/providers');
  await hubCall('/api/providers', 'PUT', {revision, providers});
  await hubCall('/api/favorites', 'PUT', {favorites: [
    {title: 'Review the diff', text: 'Review the diff on this branch.\nList the risks first, then the nits.'},
    {title: 'Tests first', text: 'Write a failing test for the bug, then make it pass.'},
  ]});
  fakeProvider = await startFakeProvider();

  browser = await chromium.launch({headless: true, executablePath: process.env.CHROMIUM_PATH || undefined,
    args: ['--no-sandbox', '--disable-dev-shm-usage']});

  // The board: what needs you first, the machines, and the list's rows of the jobs' agents.
  const page = await openHub(browser, desktop);
  await openIssues(page);
  await shot(page, 'dispatch-issues.png');
  await elementShot(page, page.locator('aside').first(), 'dispatch-list.png');

  // The repositories: the 20 most recent, or any one by its name.
  await page.getByRole('button', {name: /^Repositories:/}).click();
  await page.getByRole('menuitem', {name: /acme\/infra/}).waitFor();
  await shot(page, 'dispatch-repos.png');
  await page.keyboard.press('Escape');
  await page.getByRole('menu').waitFor({state: 'detached'});

  // New issue, written and about to go to an agent.
  await page.getByRole('button', {name: 'New issue'}).click();
  const newIssue = page.getByRole('dialog', {name: 'New issue'});
  await newIssue.getByLabel('Title').fill('Show the build\'s commit in the footer');
  await newIssue.getByLabel('Description').fill('Put the short SHA and build time in the footer, linked to the commit.\n\nDone when: the footer shows it on staging.');
  await shot(page, 'dispatch-new-issue.png');
  await page.keyboard.press('Escape');
  await newIssue.waitFor({state: 'detached'});

  // An issue's detail beside the board: an agent switch under way, three runs and a comment.
  await openCard(page, 'Settings: autosave drafts while typing');
  await shot(page, 'dispatch-detail.png');
  const panel = page.locator('aside.issue-panel');

  // A job that failed: why, its log, and Retry.
  await openCard(page, '升級到 Go 1.24');
  await panelShot(page, panel, 'dispatch-failed.png');

  // An issue in review, and the switch to another agent, which asks first.
  await openCard(page, '登入後重新導向會無限循環');
  await panelShot(page, panel, 'dispatch-review.png');
  await panel.getByRole('button', {name: 'Switch agent'}).click();
  await page.getByRole('menuitem', {name: 'Codex'}).click();
  await panel.getByRole('alertdialog', {name: 'Confirm'}).waitFor();
  await panelShot(page, panel, 'dispatch-confirm-switch.png');
  await panel.getByRole('button', {name: 'Cancel'}).click();

  // Stop asks first: the worktree and the branch go.
  await openCard(page, 'Dark mode: chart tooltips are unreadable');
  await panel.getByRole('button', {name: 'Stop', exact: true}).click();
  await panel.getByRole('alertdialog', {name: 'Confirm'}).waitFor();
  await panelShot(page, panel, 'dispatch-confirm-stop.png');
  await panel.getByRole('button', {name: 'Cancel'}).click();
  await panel.getByRole('button', {name: 'Close the issue'}).click();
  await panel.waitFor({state: 'detached'});

  // Done's Clear: the hub forgets the ended jobs, after asking.
  const done = page.locator('#issues-done');
  await done.scrollIntoViewIfNeeded();
  await done.getByRole('button', {name: 'Clear'}).click();
  const clear = page.getByRole('alertdialog', {name: /^Clear \d+ ended jobs?\?$/});
  await clear.waitFor();
  await page.waitForTimeout(400);
  await elementShot(page, clear, 'dispatch-clear.png');
  await clear.getByRole('button', {name: 'Cancel'}).click();
  await clear.waitFor({state: 'detached'});

  // The hub's settings: its GitHub App, and the model providers with their prices and health.
  await page.getByRole('button', {name: 'Hub settings', exact: true}).click();
  const settings = page.getByRole('dialog', {name: 'Hub settings'});
  await settings.getByRole('tab', {name: 'GitHub'}).click();
  await settings.getByRole('list', {name: 'Repositories of acme'}).waitFor();
  await settings.getByRole('list', {name: 'Repositories of dave'}).waitFor();
  await page.waitForFunction(() => [...document.querySelectorAll('[role=dialog] img')].every(img => img.complete && img.naturalWidth > 0));
  await elementShot(page, settings, 'hub-settings-github.png');
  await settings.getByRole('tab', {name: 'Providers'}).click();
  const gateway = settings.locator('[data-provider="gateway"]');
  await gateway.locator('[data-provider-health]').waitFor();
  await gateway.locator('[data-provider-prices] button').click();
  await gateway.getByRole('list', {name: 'Prices of LiteLLM gateway'}).waitFor();
  // The row's buttons show while it has the focus; the image shows it as the pointer left it.
  await page.evaluate(() => document.activeElement?.blur());
  await elementShot(page, settings, 'hub-settings-providers.png');
  await gateway.locator('[data-provider-prices] button').first().click();

  // A new provider's editor after Check connection, two of its models picked.
  await settings.getByRole('button', {name: 'Add'}).click();
  const editor = settings.getByRole('form', {name: 'New provider'});
  await editor.getByLabel('ID').fill('lab');
  await editor.getByLabel('Name').fill('Lab cluster');
  await editor.getByLabel('Base URL').fill(`http://127.0.0.1:${providerPort}/v1`);
  await editor.getByLabel('API key', {exact: true}).fill('sk-lab-0001');
  await editor.getByRole('button', {name: 'Check connection'}).click();
  await editor.getByText(/^Reached in \d+ ms · 6 models$/).waitFor();
  const offered = editor.getByRole('group', {name: 'The provider\'s models'});
  await offered.getByRole('button', {name: 'Kimi-K3'}).click();
  await offered.getByRole('button', {name: 'DeepSeek-V4'}).click();
  await offered.scrollIntoViewIfNeeded();
  const settingsBox = await settings.boundingBox();
  const offeredBox = await offered.boundingBox();
  const bottom = Math.min(settingsBox.y + settingsBox.height, offeredBox.y + offeredBox.height + 36);
  const top = Math.max(settingsBox.y, bottom - 900);
  await clipShot(page, 'hub-settings-provider-check.png', {x: settingsBox.x, y: top, width: settingsBox.width, height: bottom - top});
  await page.keyboard.press('Escape');
  await page.context().close();

  // Assign's menu, on machines whose long names share a start, which the menu dims.
  const longNames = {'pve-dev': 'coder-dev-pve-workstation', 'mac-mini': 'coder-dev-mac-mini', 'gpu-box': 'coder-dev-gpu-box'};
  const assignPage = await openHub(browser, desktop, {names: longNames});
  await openIssues(assignPage);
  const todo = assignPage.getByRole('list', {name: 'To do'});
  const webhookCard = todo.locator('li', {hasText: 'Rate-limit the webhook endpoint'});
  await webhookCard.scrollIntoViewIfNeeded();
  await assignPage.locator('[data-issues-view] > div').first().evaluate(board => board.scrollBy(0, 160));
  await webhookCard.getByRole('button', {name: 'Choose the agent and the machine'}).click();
  await assignPage.getByRole('menuitem', {name: /^Claude Code on coder-dev-pve-workstation$/}).waitFor();
  await shot(assignPage, 'dispatch-assign.png');
  await assignPage.context().close();

  // Issues before GitHub is connected, with the machines and no job yet.
  const noGitHub = await openHub(browser, desktop, {withJobs: false, github: false});
  await openIssues(noGitHub, {connected: false});
  await shot(noGitHub, 'dispatch-no-github.png');
  await noGitHub.context().close();

  // On a phone: the board, and an issue's detail as a sheet.
  const phonePage = await openHub(browser, phone);
  await openIssues(phonePage);
  await shot(phonePage, 'dispatch-phone.png');
  await phonePage.locator('[data-issues-view]').getByText('登入後重新導向會無限循環', {exact: true}).first().click();
  await phonePage.locator('[data-issue-detail]').getByRole('heading', {name: '登入後重新導向會無限循環'}).waitFor();
  await phonePage.waitForTimeout(700);
  await shot(phonePage, 'dispatch-phone-detail.png');
  await phonePage.context().close();

  // The chat's model menu: Claude Code's models, then the hub's providers. An AgentAPI joined to
  // the hub gets them within a minute, the first time before its agent starts.
  const bin = path.join(root, 'bin');
  await mkdir(bin, {recursive: true});
  await writeFile(path.join(bin, 'claude'), fakeClaude);
  await chmod(path.join(bin, 'claude'), 0o755);
  const chatEnv = {...env, PATH: `${bin}:${path.dirname(process.execPath)}:${env.PATH}`};
  execFileSync(binary, ['hub', 'join', `${hubAPI}/hooks`, '--secret', hubSecret, '--public-url', 'http://127.0.0.1:{port}'],
    {env: chatEnv, stdio: 'ignore'});
  const work = path.join(root, 'web');
  await mkdir(work, {recursive: true});
  startProcess('claude server', work, chatEnv, ['server', '--bind', '127.0.0.1', '--allowed-hosts', 'localhost,127.0.0.1',
    '--port', String(chatPort), '--type', 'claude', '--', 'claude']);
  const chatAPI = `http://127.0.0.1:${chatPort}`;
  await poll(async () => (await (await fetch(`${chatAPI}/status`)).json()).lifecycle === 'ready', 'the Claude Code server');
  await poll(async () => ((await (await fetch(`${chatAPI}/settings`)).json()).options?.providers ?? []).length === 2, 'the hub\'s providers');
  const chatContext = await browser.newContext({...contextOptions, ...desktop});
  chatContext.on('weberror', event => errors.push(event.error().message));
  await chatContext.addInitScript(() => { try { localStorage.setItem('theme', 'light'); } catch {} });
  await chatContext.route(url => !isLocal(url), route => route.abort());
  const chat = await chatContext.newPage();
  chat.setDefaultTimeout(20_000);
  const chatURL = new URL(`${chatAPI}/chat/`);
  chatURL.searchParams.set('url', chatAPI);
  await chat.goto(chatURL.href);
  await chat.getByRole('textbox', {name: 'Task message'}).waitFor();
  await chat.evaluate(() => document.fonts.ready);
  await chat.waitForTimeout(800);
  const composer = chat.locator('form:has(textarea[aria-label="Task message"])');
  await chat.locator('button[title="Model and reasoning effort"]').click();
  await chat.getByRole('menuitemradio', {name: /Kimi-K3/}).waitFor();
  await regionShot(chat, 'model-providers.png', [chat.getByRole('menu'), composer]);
  await chatContext.close();

  if (errors.length) throw new Error(errors.join('\n'));
  console.log(`Captured ${images.length} views with no browser errors.`);
} catch (error) {
  for (const server of servers) {
    if (server.log()) console.error(`--- ${server.name} log ---\n${server.log()}`);
  }
  throw error;
} finally {
  await browser?.close();
  fakeProvider?.close();
  for (const server of servers) {
    server.child.kill('SIGTERM');
    const timer = setTimeout(() => server.child.kill('SIGKILL'), 8000);
    await server.exited;
    clearTimeout(timer);
  }
  await rm(root, {recursive: true, force: true});
}
