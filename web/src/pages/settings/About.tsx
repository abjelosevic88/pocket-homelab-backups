import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { fmtBytes } from '../../lib/format'
import type { SystemInfo } from '../../lib/types'
import { Icon } from '../../components/icons'
import { Loading, useAsync } from '../../components/ui'
import { Section, StatusDot } from './common'

const COUNT_META: Record<string, [string, string]> = {
  jobs: ['Jobs', 'briefcase'], sources: ['Sources', 'shield'], repositories: ['Repositories', 'hard-drive'], connections: ['Connections', 'cloud'], runs: ['Runs', 'history'],
}

export function AboutTab() {
  const { isAdmin } = useApp()
  const info = useAsync(() => api.get<SystemInfo>(`${V1}/settings/system`), [])
  const d = info.data
  if (info.error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{info.error}</span></div>
  if (!d) return <Loading />
  const counts = Object.entries(d.counts || {}).filter(([k]) => COUNT_META[k]).slice(0, 4)
  const core = d.core
  const rows: [string, React.ReactNode][] = [
    ['Version', <code key="v">{d.version}</code>],
    ['Uptime', d.uptime],
    ['Go', d.go],
    ['Sign-in', d.auth_mode],
    ['Box time zone', d.timezone],
    ['Public address', d.base_url || <span className="muted">not set</span>],
  ]
  if (d.restic_host) rows.push(['Snapshots are made as', <code key="h">{d.restic_host}</code>])
  for (const [k, v] of Object.entries(d.tools || {})) rows.push([k, <code key={k}>{v}</code>])
  if (isAdmin) {
    if (d.db_size !== undefined) rows.push(['Database', fmtBytes(d.db_size)])
    if (d.users !== undefined) rows.push(['People', String(d.users)])
    if (d.data_dir) rows.push(['Data folder', <code key="dd">{d.data_dir}</code>])
  }
  return (
    <div className="stack loose">
      <div className="set-about-hero">
        <img src="/favicon.svg" alt="" width={48} height={48} />
        <div>
          <h3>Pocket Backups</h3>
          <p className="muted small">Backs up the whole box and says plainly whether it is safe — databases, folders, copies to a NAS and the clouds, an offline USB disk, and any file back from any night. Part of the Pocket apps.</p>
        </div>
      </div>
      {counts.length > 0 && <div className="set-stats">
        {counts.map(([k, n]) => (
          <div key={k} className="set-stat"><Icon name={COUNT_META[k][1]} size={16} /><span className="set-stat-n tnum">{n ?? 0}</span><span className="set-stat-l">{COUNT_META[k][0]}</span></div>
        ))}
      </div>}
      <Section title="Pocket Core">
        <div className="row">
          <StatusDot status={!core.configured ? 'off' : core.registered ? 'ok' : 'error'} />
          <span>{!core.configured ? 'Not configured on this box' : core.registered ? 'Connected' : 'Not reachable'}</span>
          {core.url && <code className="muted small">{core.url}</code>}
        </div>
        {core.error && <div className="callout danger mt-s"><Icon name="alert" size={15} /><span>{core.error}</span></div>}
      </Section>
      <Section title="This app">
        <dl className="set-dl">
          {rows.map(([k, v]) => <div key={k}><dt>{k}</dt><dd>{v}</dd></div>)}
        </dl>
      </Section>
      <div className="row wrap">
        <button className="btn" onClick={() => window.dispatchEvent(new CustomEvent('pbk:shortcuts'))}><Icon name="keyboard" size={15} />Keyboard shortcuts</button>
        <a className="btn" href="https://github.com/abjelosevic88/pocket-homelab-backups" target="_blank" rel="noreferrer"><Icon name="external" size={15} />Project on GitHub</a>
      </div>
    </div>
  )
}
