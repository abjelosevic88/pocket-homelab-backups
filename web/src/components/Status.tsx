// Small status marks: always an icon (or dot) plus words, never colour alone.
import type { Health, RunStatus, Step } from '../lib/types'
import { RUN_LABEL } from '../lib/format'
import { Icon } from './icons'

const RUN_ICON: Record<string, string> = { running: 'refresh', success: 'check-circle', warning: 'alert', failed: 'x-circle', cancelled: 'stop', skipped: 'minus' }
const RUN_LVL: Record<string, string> = { running: 'run', success: 'ok', warning: 'warn', failed: 'crit', cancelled: 'off', skipped: 'off' }

export function RunBadge({ status, sm }: { status: RunStatus | string; sm?: boolean }) {
  return <span className={`lvl-badge ${sm ? 'sm' : ''} ${RUN_LVL[status] || 'off'}`}><Icon name={RUN_ICON[status] || 'circle'} size={11} strokeWidth={2.2} className={status === 'running' ? 'spin' : ''} />{RUN_LABEL[status] || status}</span>
}

/** A dot for lists: running, success, warning, failed, or nothing yet. */
export function RunDot({ status }: { status?: RunStatus | string | null }) {
  const s = status || 'none'
  return <span className={`run-dot ${RUN_LVL[s] || 'none'}`} title={RUN_LABEL[s] || (s === 'off' ? 'Switched off' : 'Not run yet')} aria-label={RUN_LABEL[s] || s} role="img" />
}

const STEP_ICON: Record<Step['status'], string> = { ok: 'check-circle', warning: 'alert', failed: 'x-circle', skipped: 'minus', running: 'refresh' }
const STEP_LVL: Record<Step['status'], string> = { ok: 'ok', warning: 'warn', failed: 'crit', skipped: 'off', running: 'run' }
export function StepIcon({ status }: { status: Step['status'] }) {
  return <span className={`step-ico ${STEP_LVL[status]}`}><Icon name={STEP_ICON[status]} size={15} strokeWidth={2} className={status === 'running' ? 'spin' : ''} /></span>
}

export const HEALTH: Record<Health, { icon: string; lvl: string; title: string }> = {
  ok: { icon: 'shield-check', lvl: 'ok', title: 'Everything is backed up' },
  warning: { icon: 'shield-alert', lvl: 'warn', title: 'Backed up, but something needs a look' },
  error: { icon: 'shield-alert', lvl: 'crit', title: 'Backups need attention' },
  empty: { icon: 'shield', lvl: 'off', title: 'Nothing is backed up yet' },
}

export function Badge({ lvl, icon, children, title }: { lvl: 'ok' | 'warn' | 'crit' | 'off' | 'run' | 'info'; icon?: string; children: React.ReactNode; title?: string }) {
  return <span className={`lvl-badge sm ${lvl}`} title={title}>{icon && <Icon name={icon} size={11} strokeWidth={2.2} />}{children}</span>
}
