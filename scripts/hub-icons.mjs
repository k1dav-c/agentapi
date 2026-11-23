/**
 * Draws the hub's app icons in chat/public/hub/ (the web app manifest's and the iOS home screen's)
 * from its mark, HubMark in chat/src/components/hub/brand.tsx, in the light theme's colors. Run it
 * again after the mark changes: AGENTAPI_PLAYWRIGHT_MODULE=... node scripts/hub-icons.mjs
 */
import path from 'node:path';
import {chromium, repo} from './capture-common.mjs';

const out = path.join(repo, 'chat/public/hub');
const fg = "oklch(0.235 0.018 258)", bg = "oklch(0.985 0.003 255)";
const needs = "oklch(0.52 0.125 58)", working = "oklch(0.53 0.165 262)", ready = "oklch(0.5 0.11 165)";
// The HubMark (chat/src/components/hub/brand.tsx) in the light theme.
const inner = `
  <circle cx="16" cy="16" r="9.5" fill="none" stroke="${bg}" stroke-opacity=".25" stroke-width="1.2"/>
  <path d="M16 16 L9.3 9.3 M16 16 L24.6 11.2 M16 16 L13.4 25" stroke="${bg}" stroke-opacity=".45" stroke-width="1.4" stroke-linecap="round"/>
  <circle cx="9.3" cy="9.3" r="2.2" fill="${needs}"/>
  <circle cx="24.6" cy="11.2" r="2.2" fill="${working}"/>
  <circle cx="13.4" cy="25" r="2.2" fill="${ready}"/>
  <circle cx="16" cy="16" r="3.4" fill="${bg}"/>`;
const rounded = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" rx="9" fill="${fg}"/>${inner}</svg>`;
// Full bleed: iOS rounds the corners itself.
const square = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" fill="${fg}"/>${inner}</svg>`;
// Maskable: the mark inside the 80% safe zone.
const maskable = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 32 32"><rect width="32" height="32" fill="${fg}"/><g transform="translate(16 16) scale(.8) translate(-16 -16)">${inner}</g></svg>`;
const browser = await chromium.launch();
const page = await browser.newPage();
for (const [name, svg, size] of [["icon-192.png", rounded, 192], ["icon-512.png", rounded, 512], ["icon-maskable-512.png", maskable, 512], ["apple-touch-icon.png", square, 180]]) {
  await page.setViewportSize({width: size, height: size});
  await page.setContent(`<html><body style="margin:0;background:transparent">${svg.replace("<svg ", `<svg width="${size}" height="${size}" `)}</body></html>`);
  await page.screenshot({path: `${out}/${name}`, omitBackground: true, clip: {x: 0, y: 0, width: size, height: size}});
}
await browser.close();
