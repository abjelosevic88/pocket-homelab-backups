// Numbers for people: bytes, rates, percentages, durations, dates and schedules.

const UNITS = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']

/** 1536 → "1.5 KB" (binary steps, short labels). */
export function fmtBytes(n: number | null | undefined, digits = 1): string {
  if (n === null || n === undefined || !isFinite(n)) return '–'
  let v = Math.abs(n), i = 0
  while (v >= 1024 && i < UNITS.length - 1) { v /= 1024; i++ }
  const d = i === 0 ? 0 : v >= 100 ? 0 : v >= 10 ? Math.min(1, digits) : digits
  return `${n < 0 ? '-' : ''}${v.toFixed(d)} ${UNITS[i]}`
}

/** Bytes per second → "1.2 MB/s". */
export function fmtRate(n: number | null | undefined): string {
  if (n === null || n === undefined || !isFinite(n)) return '–'
  if (n < 1) return '0 B/s'
  return `${fmtBytes(n)}/s`
}

export function fmtPct(n: number | null | undefined, digits = 0): string {
  if (n === null || n === undefined || !isFinite(n)) return '–'
  return `${n.toFixed(n > 0 && n < 1 && digits === 0 ? 1 : digits)}%`
}

export function fmtNum(n: number | null | undefined, digits = 0): string {
  if (n === null || n === undefined || !isFinite(n)) return '–'
  if (Math.abs(n) >= 1e6) return `${(n / 1e6).toFixed(1)}M`
  if (Math.abs(n) >= 1e4) return `${(n / 1e3).toFixed(1)}K`
  return n.toLocaleString('en-US', { maximumFractionDigits: digits, minimumFractionDigits: digits })
}

/** Seconds → "3d 4h", "5h 12m", "42m", "18s". */
export function fmtDuration(sec: number | null | undefined): string {
  if (sec === null || sec === undefined || !isFinite(sec) || sec < 0) return '–'
  const s = Math.floor(sec)
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60)
  if (d > 0) return h ? `${d}d ${h}h` : `${d}d`
  if (h > 0) return m ? `${h}h ${m}m` : `${h}h`
  if (m > 0) return `${m}m`
  return `${s}s`
}

/** ISO or unix seconds → "just now", "3 min ago", "2 h ago", "yesterday", "Oct 12". */
export function ago(t: string | number | null | undefined): string {
  if (t === null || t === undefined || t === '' || t === 0) return 'never'
  const ms = typeof t === 'number' ? t * 1000 : Date.parse(t)
  if (isNaN(ms) || ms <= 0) return 'never'
  const s = Math.round((Date.now() - ms) / 1000)
  if (s < 45) return 'just now'
  if (s < 3600) return `${Math.max(1, Math.round(s / 60))} min ago`
  if (s < 86400) return `${Math.round(s / 3600)} h ago`
  if (s < 2 * 86400) return 'yesterday'
  if (s < 14 * 86400) return `${Math.round(s / 86400)} days ago`
  return new Date(ms).toLocaleDateString('en-US', { month: 'short', day: 'numeric', year: new Date(ms).getFullYear() === new Date().getFullYear() ? undefined : 'numeric' })
}

/** Seconds between two ISO times (to = now when empty). */
export function secondsBetween(from: string, to?: string | null): number {
  const a = Date.parse(from), b = to ? Date.parse(to) : Date.now()
  return Math.max(0, (b - a) / 1000)
}

/** A date + time, "Oct 21, 14:05". */
export function fmtDateTime(t: string | number, h12 = false): string {
  const d = typeof t === 'number' ? new Date(t * 1000) : new Date(t)
  if (isNaN(d.getTime())) return '–'
  return d.toLocaleString('en-US', { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit', hour12: h12 })
}

export function plural(n: number, one: string, many = one + 's'): string { return `${n} ${n === 1 ? one : many}` }

/** Milliseconds → "1m 12s", "840 ms". */
export function fmtMs(ms: number | null | undefined): string {
  if (ms === null || ms === undefined || !isFinite(ms) || ms < 0) return '–'
  if (ms < 1000) return `${Math.round(ms)} ms`
  const s = ms / 1000
  if (s < 60) return `${s.toFixed(s < 10 ? 1 : 0)}s`
  const m = Math.floor(s / 60), r = Math.round(s % 60)
  if (m < 60) return r ? `${m}m ${r}s` : `${m}m`
  return fmtDuration(s)
}

const sameDay = (a: Date, b: Date) => a.getFullYear() === b.getFullYear() && a.getMonth() === b.getMonth() && a.getDate() === b.getDate()

/** "Last night 04:16", "Today 10:20", "Yesterday 14:00", "Tue 21 Oct 06:15". */
export function friendlyTime(t: string | null | undefined, h12 = false): string {
  if (!t) return 'never'
  const d = new Date(t)
  if (isNaN(d.getTime()) || d.getTime() <= 0) return 'never'
  const now = new Date()
  const time = d.toLocaleTimeString('en-GB', { hour: '2-digit', minute: '2-digit', hour12: h12 })
  const y = new Date(now); y.setDate(now.getDate() - 1)
  if (sameDay(d, now)) return (d.getHours() < 7 && now.getHours() >= 7 ? 'Last night ' : 'Today ') + time
  if (sameDay(d, y)) return (d.getHours() >= 20 ? 'Last night ' : 'Yesterday ') + time
  const tm = new Date(now); tm.setDate(now.getDate() + 1)
  if (sameDay(d, tm)) return 'Tomorrow ' + time
  const opts: Intl.DateTimeFormatOptions = { weekday: 'short', day: 'numeric', month: 'short' }
  if (d.getFullYear() !== now.getFullYear()) opts.year = 'numeric'
  return d.toLocaleDateString('en-GB', opts) + ' ' + time
}

/** "in 3 h", "in 12 min", "tomorrow". */
export function until(t: string | null | undefined): string {
  if (!t) return ''
  const s = Math.round((Date.parse(t) - Date.now()) / 1000)
  if (isNaN(s)) return ''
  if (s < 60) return 'in a moment'
  if (s < 3600) return `in ${Math.round(s / 60)} min`
  if (s < 86400) return `in ${Math.round(s / 3600)} h`
  return `in ${Math.round(s / 86400)} days`
}

const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
const pad = (n: number | string) => String(n).padStart(2, '0')

/** A cron line in words ("Every day at 04:15"); falls back to the line itself. */
export function cronText(c: string | null | undefined): string {
  if (!c || !c.trim()) return 'Only when started by hand'
  const s = c.trim()
  const short: Record<string, string> = { '@hourly': 'Every hour', '@daily': 'Every day at 00:00', '@midnight': 'Every day at 00:00', '@weekly': 'Every Sunday at 00:00', '@monthly': 'On the 1st of every month at 00:00' }
  if (short[s]) return short[s]
  const f = s.split(/\s+/)
  if (f.length !== 5) return s
  const [mi, h, dom, mon, dow] = f
  if (!/^\d+$/.test(mi)) return s
  if (h === '*' && dom === '*' && mon === '*' && dow === '*') return `Every hour at :${pad(mi)}`
  if (!/^\d+$/.test(h)) return s
  const at = `${pad(h)}:${pad(mi)}`
  if (dom === '*' && mon === '*' && dow === '*') return `Every day at ${at}`
  if (dom === '*' && mon === '*' && /^\d$/.test(dow)) return `Every ${DAYS[+dow % 7]} at ${at}`
  if (/^\d+$/.test(dom) && mon === '*' && dow === '*') return `On day ${dom} of every month at ${at}`
  return s
}

export const RUN_LABEL: Record<string, string> = { running: 'Running', success: 'Done', warning: 'Done with warnings', failed: 'Failed', cancelled: 'Stopped', skipped: 'Skipped' }
export const KIND_LABEL: Record<string, string> = {
  backup: 'Backup', prep: 'Before the snapshot', post: 'After the snapshot', check: 'Check', prune: 'Prune', copy: 'Copy', restore: 'Restore',
  pull: 'Cloud pull', source: 'Try a source', hook: 'Backrest', unlock: 'Unlock', init: 'Create', stats: 'Read sizes',
}

/** When a job runs, in words: its schedule, or "When its disk is plugged in". */
export function jobWhen(job: { schedule: string; config: { run_on_connect?: boolean } }): string {
  if (!job.schedule && job.config?.run_on_connect) return 'When its disk is plugged in'
  return cronText(job.schedule)
}
