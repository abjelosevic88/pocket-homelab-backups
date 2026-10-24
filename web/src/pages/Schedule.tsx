import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { cronText, friendlyTime, until } from '../lib/format'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { Badge } from '../components/Status'
import { Card, Empty, Loading, Segmented, useAsync } from '../components/ui'

type Recurring = {
  kind: string; title: string; schedule: string; trigger?: string; next_run?: string
  job_id?: number; repository_id?: number; detail?: string; runs_by: string; enabled: boolean
}
type Upcoming = { at: string; kind: string; title: string; job_id?: number; repository_id?: number; schedule: string }
type ScheduleData = { recurring: Recurring[]; upcoming: Upcoming[]; timezone: string; days: number }

const KIND: Record<string, [string, string]> = {
  backup: ['archive', 'Backup'], pull: ['download', 'Cloud pull'], check: ['shield', 'Check'], prune: ['trash', 'Prune'],
}

const link = (x: { job_id?: number; repository_id?: number }) => x.job_id ? `/jobs/${x.job_id}` : x.repository_id ? `/places/repositories/${x.repository_id}` : '/'

function dayLabel(t: string): string {
  const d = new Date(t)
  const today = new Date(); today.setHours(0, 0, 0, 0)
  const day = new Date(d); day.setHours(0, 0, 0, 0)
  const diff = Math.round((day.getTime() - today.getTime()) / 86400000)
  if (diff === 0) return 'Today'
  if (diff === 1) return 'Tomorrow'
  return d.toLocaleDateString('en-GB', { weekday: 'long', day: 'numeric', month: 'long' })
}

function clock(t: string, h12: boolean): string {
  return new Date(t).toLocaleTimeString(h12 ? 'en-US' : 'en-GB', { hour: '2-digit', minute: '2-digit', hour12: h12 })
}

export default function Schedule() {
  const { version, h12 } = useApp()
  const [days, setDays] = useState(7)
  const sc = useAsync(() => api.get<ScheduleData>(`${V1}/schedule?days=${days}`), [version, days])
  const d = sc.data
  const groups: [string, Upcoming[]][] = []
  for (const u of d?.upcoming || []) {
    const label = dayLabel(u.at)
    const last = groups[groups.length - 1]
    if (last && last[0] === label) last[1].push(u)
    else groups.push([label, [u]])
  }
  const clocked = (d?.recurring || []).filter(r => r.schedule)
  const other = (d?.recurring || []).filter(r => !r.schedule)
  return (
    <Shell title="Schedule" loading={sc.loading && !!d} controls={<Segmented size="sm" value={days} onChange={v => setDays(v)} options={[{ value: 7, label: '7 days' }, { value: 14, label: '14 days' }, { value: 31, label: '31 days' }]} />}>
      {!d ? <Loading /> : (
        <div className="stack loose">
          <p className="muted" style={{ margin: 0 }}>Everything that runs on its own: backups, cloud pulls, and the checks and prunes that keep the repositories healthy. Times are shown in your browser’s time zone; the box runs on {d.timezone}.</p>
          <Card title={<span className="row"><Icon name="clock" size={16} />Recurring</span>} flush>
            {d.recurring.length === 0 ? <div className="card-body"><Empty title="Nothing runs on its own yet" icon="clock">Give a job a schedule and it shows up here.</Empty></div> : (
              <div className="run-list">
                {[...clocked, ...other].map((r, i) => {
                  const [icon, label] = KIND[r.kind] || ['clock', r.kind]
                  return (
                    <Link key={i} to={link(r)} className="run-row">
                      <span className="repo-ico"><Icon name={icon} size={15} /></span>
                      <span className="grow min0">
                        <span className="run-title ellipsis">{r.title}</span>
                        <span className="run-sub muted small ellipsis">{r.schedule ? cronText(r.schedule) : r.trigger}{r.detail ? ` · ${r.detail}` : ''}</span>
                      </span>
                      <span className="row" style={{ gap: 6, flexShrink: 0 }}>
                        <Badge lvl="info">{label}</Badge>
                        {r.runs_by === 'backrest' && <Badge lvl="warn">by Backrest</Badge>}
                        {!r.enabled && <Badge lvl="off">off</Badge>}
                      </span>
                      <span className="run-when muted small">
                        {r.next_run ? <><span>{friendlyTime(r.next_run, h12)}</span><span>{until(r.next_run)}</span></> : <span>—</span>}
                      </span>
                    </Link>
                  )
                })}
              </div>
            )}
          </Card>
          <Card title={<span className="row"><Icon name="calendar" size={16} />Coming up</span>} sub={`The next ${d.days} days`} flush>
            {groups.length === 0 ? <div className="card-body muted small">Nothing scheduled in the next {d.days} days.</div> : groups.map(([label, items]) => (
              <div key={label}>
                <div className="sched-day">{label}</div>
                <div className="run-list">
                  {items.map((u, i) => {
                    const [icon, kindLabel] = KIND[u.kind] || ['clock', u.kind]
                    return (
                      <Link key={i} to={link(u)} className="run-row">
                        <span className="sched-time tnum">{clock(u.at, h12)}</span>
                        <Icon name={icon} size={15} className="muted" />
                        <span className="grow min0 ellipsis">{u.title}</span>
                        <span className="muted xsmall">{kindLabel}</span>
                      </Link>
                    )
                  })}
                </div>
              </div>
            ))}
          </Card>
        </div>
      )}
    </Shell>
  )
}
