// Cards and rows the pages share: a job, a repository, a run, a connection.
import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useActions } from '../lib/actions'
import { ago, cronText, fmtBytes, fmtMs, friendlyTime, KIND_LABEL, until, jobWhen } from '../lib/format'
import type { Connection, Job, Repository, Run, RunDetail } from '../lib/types'
import { CONN_ICON, CONN_NAME } from '../pages/connKinds'
import { Icon } from './icons'
import { Badge, RunBadge, RunDot } from './Status'

/** "Copying to NAS · 42%" for a run in progress, refreshed every 3 s. */
export function LiveProgress({ runId }: { runId: number }) {
  const [d, setD] = useState<RunDetail | null>(null)
  useEffect(() => {
    let stop = false
    const tick = () => api.get<RunDetail>(`${V1}/runs/${runId}`).then(x => { if (!stop) setD(x) }).catch(() => {})
    tick()
    const t = setInterval(tick, 3000)
    return () => { stop = true; clearInterval(t) }
  }, [runId])
  const pct = d?.percent ? Math.round(d.percent * 100) : null
  return (
    <Link to={`/runs/${runId}`} className="live">
      <span className="live-line"><Icon name="refresh" size={13} className="spin" /><span className="ellipsis">{d?.step || 'Starting…'}</span>{pct !== null && pct > 0 && pct < 100 && <span className="tnum muted">{pct}%</span>}</span>
      <span className="live-bar"><span style={{ width: `${pct ?? 0}%` }} className={pct ? '' : 'indeterminate'} /></span>
    </Link>
  )
}

export function JobCard({ job }: { job: Job }) {
  const { h12 } = useApp()
  const { runJob } = useActions()
  const last = job.last_run
  return (
    <div className={`job-card ${job.enabled ? '' : 'off'}`}>
      <div className="row">
        <RunDot status={job.running ? 'running' : job.enabled ? last?.status : 'off'} />
        <Link to={`/jobs/${job.id}`} className="job-name grow ellipsis">{job.name}</Link>
        {job.engine === 'backrest' && <Badge lvl="info" icon="archive">Backrest</Badge>}
        {job.kind === 'pull' && <Badge lvl="info" icon="download">Cloud pull</Badge>}
        {!job.enabled && <Badge lvl="off">Off</Badge>}
      </div>
      <div className="job-meta muted small">
        <span><Icon name="clock" size={12} />{jobWhen(job)}</span>
        {job.next_run && <span>next {friendlyTime(job.next_run, h12)} ({until(job.next_run)})</span>}
      </div>
      {job.running ? <LiveProgress runId={job.running} /> : last ? (
        <Link to={`/runs/${last.id}`} className="job-last">
          <RunBadge status={last.status} sm />
          <span className="muted small">{friendlyTime(last.finished_at || last.started_at, h12)}</span>
          <span className="small ellipsis grow">{last.status === 'failed' ? last.error : last.summary}</span>
        </Link>
      ) : <div className="muted small job-last">Has not run yet.</div>}
      <div className="row job-actions">
        {job.last_good && last?.status !== 'success' && <span className="muted xsmall">last good {ago(job.last_good.finished_at)}</span>}
        <span className="spacer" />
        <button className="btn sm" disabled={!!job.running} onClick={() => runJob(job.id)}><Icon name="play" size={13} />Run now</button>
      </div>
    </div>
  )
}

export function RepoCard({ repo }: { repo: Repository }) {
  const { h12 } = useApp()
  const st = repo.stats
  const icon = repo.location === 'usb' ? 'usb' : repo.location === 'connection' ? 'cloud' : repo.location === 'uri' ? 'server' : 'hard-drive'
  return (
    <Link to={`/places/repositories/${repo.id}`} className="repo-card">
      <div className="row">
        <span className="repo-ico"><Icon name={icon} size={17} /></span>
        <span className="grow ellipsis repo-name">{repo.name}</span>
        {repo.managed_by === 'backrest' && <Badge lvl="info" icon="archive">Backrest</Badge>}
      </div>
      <div className="repo-nums">
        <div><b className="tnum">{st.updated_at ? fmtBytes(st.size) : '–'}</b><span>stored</span></div>
        <div><b className="tnum">{st.updated_at ? st.snapshots : '–'}</b><span>snapshots</span></div>
        <div><b>{st.latest ? friendlyTime(st.latest, h12) : '–'}</b><span>newest</span></div>
      </div>
      <div className="row wrap repo-badges">
        {repo.copies_paused && <Badge lvl="crit" icon="pause" title={repo.paused_reason}>Copies paused</Badge>}
        {repo.status === 'error' && <Badge lvl="warn" icon="alert" title={repo.last_error}>Does not open</Badge>}
        {repo.location === 'usb' && (repo.disk_connected ? <Badge lvl={repo.disk_mounted ? 'run' : 'ok'} icon="plug">{repo.disk_mounted ? 'Plugged in · mounted' : 'Plugged in'}</Badge> : <Badge lvl="off" icon="usb">Unplugged</Badge>)}
        {st.disk_used_pct ? <span className="muted xsmall">disk {st.disk_used_pct}% full</span> : null}
      </div>
    </Link>
  )
}

export function RunRow({ run, showJob = true }: { run: Run; showJob?: boolean }) {
  const { h12 } = useApp()
  return (
    <Link to={`/runs/${run.id}`} className="run-row">
      <RunBadge status={run.status} sm />
      <span className="grow min0">
        <span className="run-title ellipsis">{showJob ? run.title : (KIND_LABEL[run.kind] || run.kind)}</span>
        <span className="run-sub muted small ellipsis">{run.status === 'failed' || run.status === 'cancelled' ? run.error : run.summary || (KIND_LABEL[run.kind] || run.kind)}</span>
      </span>
      <span className="run-when muted small">
        <span>{friendlyTime(run.started_at, h12)}</span>
        <span className="tnum">{run.status === 'running' ? 'running…' : fmtMs(run.duration_ms)}</span>
      </span>
    </Link>
  )
}

export function ConnBadge({ c }: { c: Connection }) {
  return (
    <span className="row" style={{ gap: 6 }}>
      <Icon name={CONN_ICON[c.kind]} size={14} />
      <span className="ellipsis">{c.name}</span>
      <span className="muted xsmall">{CONN_NAME[c.kind]}</span>
    </span>
  )
}
