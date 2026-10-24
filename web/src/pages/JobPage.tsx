import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, qs, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useActions } from '../lib/actions'
import { useData } from '../lib/data'
import { ago, cronText, fmtBytes, fmtMs, friendlyTime, until } from '../lib/format'
import type { BackrestInfo, Job, Retention, Run } from '../lib/types'
import { LiveProgress, RunRow } from '../components/cards'
import { openCopyEditor, openJobEditor, openSourceEditor } from '../components/dialogs'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { Badge, RunBadge } from '../components/Status'
import { Card, Confirm, Empty, errMsg, Loading, MenuButton, Switch, useAsync, useToast } from '../components/ui'
import { CONN_ICON } from './connKinds'
import { KIND_ICON, KIND_ONE } from './sourceKinds'

export function retentionText(r: Retention): string {
  if (r.keep_all) return 'Every snapshot is kept'
  const parts: string[] = []
  const add = (n: number | undefined, w: string) => { if (n) parts.push(`${n} ${w}`) }
  add(r.keep_last, 'newest'); add(r.keep_hourly, 'hourly'); add(r.keep_daily, 'daily'); add(r.keep_weekly, 'weekly'); add(r.keep_monthly, 'monthly'); add(r.keep_yearly, 'yearly')
  if (!parts.length) return 'Every snapshot is kept (no rule set)'
  return 'Keeps ' + parts.join(', ') + (r.prune ? '; space freed right away' : '') + (r.group_by_none ? '; all snapshots counted together' : '')
}

export default function JobPage() {
  const { id } = useParams()
  const { jobs, sources, repos, conns, loaded, reload } = useData()
  const { version, h12, bump } = useApp()
  const { runJob } = useActions()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [del, setDel] = useState(false)
  const job = jobs.find(j => String(j.id) === id)
  const runs = useAsync(() => api.get<Run[]>(`${V1}/runs${qs({ job: id, limit: 25 })}`), [id, version])
  const br = useAsync(() => job?.engine === 'backrest' ? api.get<BackrestInfo>(`${V1}/backrest`) : Promise.resolve(null), [job?.engine, job?.backrest_plan])

  if (!loaded) return <Shell title="Job"><Loading /></Shell>
  if (!job) return <Shell title="Job"><Empty title="This job is gone" icon="briefcase"><Link to="/jobs">All jobs</Link></Empty></Shell>

  const repo = repos.find(r => r.id === job.repository_id)
  const mine = sources.filter(s => job.source_ids.includes(s.id))
  const plan = br.data?.plans.find(p => p.id === job.backrest_plan)
  const pull = job.config.pull
  const pullConn = conns.find(c => c.id === pull?.connection_id)

  const toggle = async (v: boolean) => {
    try { await api.put<Job>(`${V1}/jobs/${job.id}`, { enabled: v }); await reload(); bump() } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async () => {
    try { await api.del(`${V1}/jobs/${job.id}`); toast('Job deleted — its snapshots stay in the repository'); await reload(); navigate('/jobs') } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <Shell title={job.name} sub={job.engine === 'backrest' ? `Backrest plan ${job.backrest_plan}` : job.kind === 'pull' ? 'Cloud folder brought home' : undefined}
      controls={<>
        <button className="btn primary sm" disabled={!!job.running} onClick={() => runJob(job.id, true)}><Icon name="play" size={13} />Run now</button>
        <button className="btn sm" onClick={() => openJobEditor(job)}><Icon name="edit" size={13} />Edit</button>
        <MenuButton label="More" align="right" items={[
          { label: 'Show its runs', icon: 'history', onSelect: () => navigate(`/runs?job=${job.id}`) },
          'sep', { label: 'Delete job', icon: 'trash', danger: true, onSelect: () => setDel(true) },
        ]}><Icon name="more" size={16} /></MenuButton>
      </>}>
      <div className="stack loose">
        <section className="job-head">
          <div className="row wrap">
            {job.engine === 'backrest' ? <Badge lvl="info" icon="archive">Runs on Backrest</Badge> : <Badge lvl="info" icon="shield">Runs on Pocket Backups</Badge>}
            <span className="muted small"><Icon name="clock" size={12} /> {cronText(job.schedule)}</span>
            {job.next_run && <span className="muted small">next {friendlyTime(job.next_run, h12)} ({until(job.next_run)})</span>}
            <span className="spacer" />
            <label className="inline-switch small"><Switch size="sm" checked={job.enabled} onChange={toggle} label="Switched on" />{job.enabled ? 'On' : 'Off'}</label>
          </div>
          {job.running ? <LiveProgress runId={job.running} /> : job.last_run && (
            <Link to={`/runs/${job.last_run.id}`} className="job-last big">
              <RunBadge status={job.last_run.status} />
              <span className="muted small">{friendlyTime(job.last_run.finished_at || job.last_run.started_at, h12)} · {fmtMs(job.last_run.duration_ms)}</span>
              <span className="small grow ellipsis">{job.last_run.status === 'failed' ? job.last_run.error : job.last_run.summary}</span>
            </Link>
          )}
          {job.last_good && job.last_run?.status !== 'success' && <span className="muted small">Last good run {ago(job.last_good.finished_at)}.</span>}
        </section>

        <div className="two-col top">
          {job.kind === 'backup' ? (
            <Card title="What’s included" actions={<button className="btn ghost sm" onClick={() => openSourceEditor(undefined, { jobs: [job.id] })}><Icon name="plus" size={13} />Add</button>} flush>
              {mine.length === 0 && !job.config.paths?.length && !plan && <div className="card-pad muted small">Nothing yet — add sources (folders, databases, volumes).</div>}
              <div className="mini-list">
                {mine.map(s => (
                  <button key={s.id} type="button" className="mini-row" onClick={() => openSourceEditor(s)}>
                    <Icon name={KIND_ICON[s.kind]} size={14} className="muted" />
                    <span className="grow min0"><span className="ellipsis block">{s.name}</span><span className="muted xsmall">{KIND_ONE[s.kind]}{s.last_size ? ` · ${fmtBytes(s.last_size)}` : ''}</span></span>
                    {!s.enabled ? <Badge lvl="off">Off</Badge> : s.last_error ? <Badge lvl="crit" icon="x-circle" title={s.last_error}>Failed</Badge> : s.last_ok_at ? <span className="muted xsmall">{ago(s.last_ok_at)}</span> : <span className="faint xsmall">not yet</span>}
                  </button>
                ))}
                {(job.config.paths || []).map(p => <div key={p} className="mini-row"><Icon name="folder" size={14} className="muted" /><span className="mono small ellipsis grow">{p}</span></div>)}
              </div>
              {(job.config.excludes?.length || 0) > 0 && <div className="card-pad muted xsmall">Leaves out: {job.config.excludes!.join(', ')}</div>}
              {job.engine === 'backrest' && (
                <div className="card-pad">
                  <div className="muted xsmall upper">Folders the Backrest plan backs up</div>
                  {br.loading ? <Loading /> : plan ? (
                    <div className="path-chips">{plan.paths.map(p => <code key={p}>{p}</code>)}{plan.excludes > 0 && <span className="muted xsmall">· {plan.excludes} exclude rule(s)</span>}</div>
                  ) : <span className="muted small">{br.data?.error || 'The plan was not found in Backrest.'}</span>}
                </div>
              )}
              {(job.config.require_mounts?.length || 0) > 0 && <div className="card-pad muted xsmall">Refuses to run unless mounted: {job.config.require_mounts!.join(', ')}</div>}
            </Card>
          ) : (
            <Card title="What it brings home">
              <dl className="kv">
                <div><dt>From</dt><dd>{pullConn ? <span className="row"><Icon name={CONN_ICON[pullConn.kind]} size={14} />{pullConn.name}</span> : 'a missing connection'} <code>{pull?.remote_path || '(top)'}</code></dd></div>
                <div><dt>To</dt><dd><code>{pull?.local_path}</code></dd></div>
                {pull?.shared_with_me && <div><dt>Shared with me</dt><dd><code>{pull.shared_path}</code></dd></div>}
                <div><dt>Mode</dt><dd>{pull?.mirror ? 'Mirror (deletes here what was deleted there)' : 'Copy (never deletes here)'}</dd></div>
              </dl>
            </Card>
          )}

          {job.kind === 'backup' && (
            <Card title="Where it goes" actions={<button className="btn ghost sm" onClick={() => openCopyEditor(job)}><Icon name="plus" size={13} />Add a copy</button>} flush>
              <div className="mini-list">
                {repo ? (
                  <Link to={`/places/repositories/${repo.id}`} className="mini-row">
                    <Icon name={repo.location === 'usb' ? 'usb' : repo.location === 'connection' ? 'cloud' : 'hard-drive'} size={14} className="muted" />
                    <span className="grow min0"><span className="block ellipsis">{repo.name}</span><span className="muted xsmall">The repository · {repo.stats.updated_at ? `${fmtBytes(repo.stats.size)}, ${repo.stats.snapshots} snapshots` : 'not read yet'}</span></span>
                    {repo.copies_paused && <Badge lvl="crit" icon="pause" title={repo.paused_reason}>Copies paused</Badge>}
                  </Link>
                ) : <div className="card-pad muted small">No repository chosen.</div>}
                {job.copies.map(c => {
                  const conn = conns.find(x => x.id === c.connection_id)
                  const target = repos.find(x => x.id === c.target_repository_id)
                  return (
                    <button key={c.id} type="button" className={`mini-row ${c.enabled ? '' : 'dim'}`} onClick={() => openCopyEditor(job, c)}>
                      <Icon name={c.mode === 'restic' ? 'archive' : conn ? CONN_ICON[conn.kind] : 'folder'} size={14} className="muted" />
                      <span className="grow min0">
                        <span className="block ellipsis">Copy to {c.name}</span>
                        <span className="muted xsmall ellipsis block">{c.mode === 'restic' ? `snapshots into ${target?.name || 'a missing repository'}` : `${conn ? conn.name + ': ' : ''}${c.path || '(top)'}`}</span>
                      </span>
                      {!c.enabled ? <Badge lvl="off">Off</Badge> : c.last_error ? <Badge lvl="crit" icon="x-circle" title={c.last_error}>Failed</Badge> : c.last_ok_at ? <span className="muted xsmall">{ago(c.last_ok_at)} · {fmtMs(c.last_duration_ms)}</span> : <span className="faint xsmall">not yet</span>}
                    </button>
                  )
                })}
              </div>
            </Card>
          )}

          <Card title="Schedule and keeping">
            <dl className="kv">
              <div><dt>When</dt><dd>{cronText(job.schedule)}{job.engine === 'backrest' ? ' (Backrest’s schedule)' : ''}</dd></div>
              {job.config.run_on_connect && <div><dt>USB disk</dt><dd>Starts when the disk is plugged in{job.config.run_on_connect_days ? ` (at most every ${job.config.run_on_connect_days} days)` : ''}</dd></div>}
              {job.kind === 'backup' && <div><dt>Keeping</dt><dd>{retentionText(job.config.retention)}{job.config.forget_before_copies ? ', before the copies' : ''}</dd></div>}
              {job.config.staging_dir && <div><dt>Staging</dt><dd><code>{job.config.staging_dir}</code></dd></div>}
              {(job.config.check_read_pct || 0) > 0 && <div><dt>Check</dt><dd>Reads {job.config.check_read_pct}% back after each run</dd></div>}
            </dl>
          </Card>

          <Card title="Telling people">
            <dl className="kv">
              <div><dt>Failures</dt><dd>{job.config.notify_failure ? 'Sent' : 'Not sent'}</dd></div>
              <div><dt>Good runs</dt><dd>{job.config.notify_success ? 'Sent' : 'Not sent'}</dd></div>
              <div><dt>Heartbeat</dt><dd>{job.config.heartbeat_url ? <code className="ellipsis">{job.config.heartbeat_url.replace(/\?.*/, '?…')}</code> : 'None'}</dd></div>
              {job.kind === 'backup' && <div><dt>Extras</dt><dd>{[job.config.verify_after && 'read back', job.config.coverage_check && 'coverage check', job.config.report && 'Homepage report', job.config.exclude_caches && 'skips caches'].filter(Boolean).join(', ') || 'none'}</dd></div>}
            </dl>
          </Card>
        </div>

        <Card title="Runs" actions={<Link to={`/runs?job=${job.id}`} className="btn ghost sm">All</Link>} flush>
          {!runs.data ? <Loading /> : runs.data.length === 0 ? <Empty title="No runs yet" icon="history" /> : <div className="run-list">{runs.data.map(r => <RunRow key={r.id} run={r} showJob={false} />)}</div>}
        </Card>
      </div>
      {del && <Confirm title={`Delete ${job.name}?`} message="The job, its copies and its schedule go away. The snapshots it made stay in the repository, and its runs stay in the history." onConfirm={remove} onCancel={() => setDel(false)} />}
    </Shell>
  )
}
