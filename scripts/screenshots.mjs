// README screenshots and a browser smoke test against the demo (scripts/demo/make.sh):
//
//   DEMO_DIR=/tmp/pocket-demo scripts/demo/make.sh
//   CDP=http://127.0.0.1:19251 BASE=http://127.0.0.1:18299 node scripts/screenshots.mjs   # any Chrome with remote debugging
//   SHOTS=0 … node scripts/screenshots.mjs                                                  # checks only
import { createRequire } from 'node:module'
import assert from 'node:assert/strict'

const require = createRequire(new URL('../web/package.json', import.meta.url))
const { chromium } = require('playwright-core')
const base = process.env.BASE || 'http://127.0.0.1:18299'
const shots = process.env.SHOTS !== '0'
const dir = new URL('../docs/screenshots/', import.meta.url).pathname
const browser = process.env.CDP ? await chromium.connectOverCDP(process.env.CDP) : await chromium.launch()
const errors = []

async function session({ phone = false, dark = false } = {}) {
  const ctx = await browser.newContext({
    viewport: phone ? { width: 390, height: 844 } : { width: 1440, height: 900 },
    deviceScaleFactor: phone ? 3 : 2, isMobile: phone, hasTouch: phone, colorScheme: dark ? 'dark' : 'light', timezoneId: 'Europe/Berlin',
  })
  const page = await ctx.newPage()
  page.on('pageerror', e => errors.push(`pageerror: ${e}`))
  page.on('console', m => { if (m.type() === 'error' && !/409|Failed to load resource/.test(m.text())) errors.push(m.text()) })
  await page.goto(base + '/login')
  const res = await page.evaluate(() => fetch('/api/v1/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ email: 'ana@example.com', password: 'demo-password' }) }).then(r => r.status))
  assert.equal(res, 200, 'demo sign-in')
  return { ctx, page }
}
async function go(page, path) { await page.goto(base + path, { waitUntil: 'networkidle' }); await page.evaluate(() => document.fonts.ready); await page.waitForTimeout(1200) }
async function shot(page, name) { if (shots) await page.screenshot({ path: dir + name + '.png' }) }
const api = (page, path) => page.evaluate(p => fetch('/api/v1' + p).then(r => r.json()), path)

{
  const { ctx, page } = await session()
  await go(page, '/')
  assert.match(await page.innerText('body'), /Nightly/, 'the nightly job on the overview')
  await shot(page, 'desktop-overview')
  const jobs = await api(page, '/jobs')
  const nightly = jobs.find(j => j.name === 'Nightly')
  await go(page, `/jobs/${nightly.id}`)
  assert.match(await page.innerText('body'), /Backblaze B2/, 'copies on the job page')
  await shot(page, 'desktop-job')
  const runs = await api(page, `/runs?job=${nightly.id}&kind=backup&limit=1`)
  await go(page, `/runs/${runs[0].id}`)
  assert.match(await page.innerText('body'), /Copy to Google Drive/, 'the steps of a run')
  await shot(page, 'desktop-run')
  const repos = await api(page, '/repositories')
  await go(page, `/restore?repo=${repos.find(r => r.name === 'This box').id}`)
  await page.locator('.snap-row').first().click()
  await page.waitForTimeout(800)
  for (const part of (process.env.DEMO_DIR || '/tmp/pocket-demo').split('/').filter(Boolean).concat(['files', 'Photos', '2026', 'Summer in Kotor'])) {
    await page.locator('.file-name', { hasText: part }).first().click()
    await page.waitForTimeout(500)
  }
  await page.locator('.file-row input[type=checkbox]').nth(2).check()
  await page.locator('.file-row input[type=checkbox]').nth(3).check()
  await page.waitForTimeout(400)
  await shot(page, 'desktop-restore')
  await go(page, '/places')
  await shot(page, 'desktop-places')
  await go(page, '/sources')
  await shot(page, 'desktop-sources')
  await go(page, '/coverage')
  assert.match(await page.innerText('body'), /uptime-kuma/, 'an uncovered app on the coverage page')
  await shot(page, 'desktop-coverage')
  for (const p of ['/jobs', '/runs', '/backrest', '/settings']) await go(page, p)
  await ctx.close()
}
{
  const { ctx, page } = await session({ dark: true })
  await go(page, '/')
  await shot(page, 'desktop-overview-dark')
  await ctx.close()
}
{
  const { ctx, page } = await session({ phone: true })
  await go(page, '/')
  await shot(page, 'phone-overview')
  const runs = await api(page, '/runs?kind=backup&limit=1')
  await go(page, `/runs/${runs[0].id}`)
  await shot(page, 'phone-run')
  await ctx.close()
}
await browser.close().catch(() => {})
if (errors.length) { console.error(errors.join('\n')); process.exit(1) }
console.log('screenshots ok')
