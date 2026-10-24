import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { ago, friendlyTime } from '../lib/format'
import type { Issue, Overview as OverviewData } from '../lib/types'
import { JobCard, RepoCard, RunRow } from '../components/cards'
import { openJobEditor, openRepoEditor, openSourceEditor } from '../components/dialogs'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { HEALTH } from '../components/Status'
import { Card, Empty, Loading, useAsync } from '../components/ui'

function sentence(d: OverviewData): string {
  const jobs = d.jobs || []
  let last = d.last_backup || ''
  for (const j of jobs) if (j.last_good && j.kind === 'backup' && j.last_good.finished_at > last) last = j.last_good.finished_at
  const when = last ? `Last good backup ${ago(last)}.` : 'No backup has finished yet.'
  switch (d.health) {
    case 'ok': return `Everything is backed up. ${when}`
    case 'warning': return `${when} A few things need a look.`
    case 'error': return `${when} Something went wrong — see below.`
    default: return 'Nothing is backed up yet. Start by adding a place for backups and a job.'
  }
}

export default function Overview() {
  const { version, isAdmin, h12 } = useApp()
  const ov = useAsync(() => api.get<OverviewData>(`${V1}/overview`), [version])
  const d = ov.data
  const h = d ? HEALTH[d.health] : null
  const issues = Array.isArray(d?.issues) ? d.issues as Issue[] : []

  return (
    <Shell title="Overview" loading={ov.loading && !!d}>
      {!d ? (ov.error ? <div className="callout danger"><Icon name="alert" size={15} /><span>{ov.error}</span></div> : <Loading />) : (
        <div className="stack loose">
          <section className={`hero ${h!.lvl}`}>
            <span className="hero-ico"><Icon name={h!.icon} size={30} strokeWidth={1.6} /></span>
            <div className="grow">
              <h2>{h!.title}</h2>
              <p>{sentence(d)}</p>
              {!isAdmin && typeof d.issues === 'number' && d.issues > 0 && <p className="muted small">{d.issues} thing(s) need an admin’s attention.</p>}
            </div>
          </section>

          {issues.length > 0 && (
            <div className="issues">
              {issues.map((i, n) => {
                const body = <><Icon name={i.level === 'error' ? 'x-circle' : 'alert'} size={15} /><span className="grow">{i.text}</span>{i.link && <Icon name="chevron-right" size={14} className="faint" />}</>
                return i.link ? <Link key={n} to={i.link} className={`issue ${i.level}`}>{body}</Link> : <div key={n} className={`issue ${i.level}`}>{body}</div>
              })}
            </div>
          )}

          {isAdmin && d.health === 'empty' && (
            <Card title="Get started">
              <ol className="steps-list">
                <li><b>Choose where backups go.</b> A folder on this box, a USB disk, a NAS, or a cloud such as Google Drive. <button className="link-btn" onClick={() => openRepoEditor()}>Add a repository</button></li>
                <li><b>Say what to protect.</b> Folders, databases, Docker volumes. <button className="link-btn" onClick={() => openSourceEditor()}>Add a source</button> or let <Link to="/sources">What’s protected</Link> find them for you.</li>
                <li><b>Make a job</b> that puts them together on a schedule. <button className="link-btn" onClick={() => openJobEditor()}>New job</button></li>
              </ol>
            </Card>
          )}

          {isAdmin && (d.jobs?.length || 0) > 0 && (
            <section>
              <div className="sec-head"><h2>Jobs</h2><Link to="/jobs" className="small">All jobs</Link></div>
              <div className="card-grid">{d.jobs!.map(j => <JobCard key={j.id} job={j} />)}</div>
            </section>
          )}

          {isAdmin && (d.jobs?.length || 0) > 0 && <ComingUp />}

          {isAdmin && (d.repositories?.length || 0) > 0 && (
            <section>
              <div className="sec-head"><h2>Where backups are kept</h2><Link to="/places" className="small">Places</Link></div>
              <div className="card-grid">{d.repositories!.map(r => <RepoCard key={r.id} repo={r} />)}</div>
            </section>
          )}

          {isAdmin && (
            <Card title="Recent runs" actions={<Link to="/runs" className="btn ghost sm">All runs</Link>} flush>
              {(d.recent_runs || []).length === 0 ? <Empty title="Nothing has run yet" icon="history" /> : (
                <div className="run-list">{d.recent_runs!.map(r => <RunRow key={r.id} run={r} />)}</div>
              )}
            </Card>
          )}
          {!isAdmin && d.last_backup && <p className="muted small">Last good backup: {friendlyTime(d.last_backup, h12)}.</p>}
        </div>
      )}
    </Shell>
  )
}

type Upcoming = { at: string; kind: string; title: string; job_id?: number; repository_id?: number }

// The next few things that run on their own (the full list is on Schedule).
function ComingUp() {
  const { version, h12 } = useApp()
  const sc = useAsync(() => api.get<{ upcoming: Upcoming[] }>(`${V1}/schedule?days=3`), [version])
  const items = (sc.data?.upcoming || []).slice(0, 5)
  if (!sc.data) return null
  return (
    <Card title={<span className="row"><Icon name="clock" size={16} />Coming up</span>} actions={<Link to="/schedule" className="btn ghost sm">Schedule</Link>} flush>
      {items.length === 0 ? <div className="card-body muted small">Nothing runs on its own in the next three days.</div> : (
        <div className="run-list">
          {items.map((u, i) => (
            <Link key={i} to={u.job_id ? `/jobs/${u.job_id}` : u.repository_id ? `/places/repositories/${u.repository_id}` : '/schedule'} className="run-row">
              <Icon name={u.kind === 'check' ? 'shield' : u.kind === 'prune' ? 'trash' : u.kind === 'pull' ? 'download' : 'archive'} size={15} className="muted" />
              <span className="grow min0 ellipsis">{u.title}</span>
              <span className="muted small nowrap">{friendlyTime(u.at, h12)}</span>
            </Link>
          ))}
        </div>
      )}
    </Card>
  )
}
