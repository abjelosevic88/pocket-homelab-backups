import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useData } from '../lib/data'
import { cronText, fmtBytes, friendlyTime, until } from '../lib/format'
import type { BackrestInfo, BROp, HookChange } from '../lib/types'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { Badge } from '../components/Status'
import { Card, Empty, errMsg, Loading, Modal, Spinner, useAsync, useToast } from '../components/ui'

const OP_LVL: Record<string, 'ok' | 'warn' | 'crit' | 'off' | 'run'> = { success: 'ok', warning: 'warn', error: 'crit', inprogress: 'run', pending: 'off', user_cancelled: 'off', system_cancelled: 'off' }

function OpBadge({ op }: { op: BROp }) {
  return <Badge lvl={OP_LVL[op.status] || 'off'}>{op.status.replace('_', ' ')}</Badge>
}

export default function Backrest() {
  const { version, h12, bump } = useApp()
  const { jobs, reload } = useData()
  const { toast } = useToast()
  const br = useAsync(() => api.get<BackrestInfo>(`${V1}/backrest`), [version])
  const [importing, setImporting] = useState(false)
  const [preview, setPreview] = useState<HookChange[] | null>(null)
  const [installing, setInstalling] = useState(false)
  const d = br.data

  const doImport = async () => {
    setImporting(true)
    try { const r = await api.post<{ repositories: number; jobs: number }>(`${V1}/backrest/import`); toast(`Added ${r.repositories} repository(ies) and ${r.jobs} job(s)`); await reload(); bump() } catch (e) { toast(errMsg(e), 'err') } finally { setImporting(false) }
  }
  const showHooks = async () => {
    try { setPreview((await api.get<{ changes: HookChange[] }>(`${V1}/backrest/hooks`)).changes) } catch (e) { toast(errMsg(e), 'err') }
  }
  const install = async () => {
    setInstalling(true)
    try { await api.post(`${V1}/backrest/hooks`); toast('Hooks installed in Backrest'); setPreview(null); br.reload() } catch (e) { toast(errMsg(e), 'err') } finally { setInstalling(false) }
  }

  return (
    <Shell title="Backrest" loading={br.loading && !!d} controls={d?.configured ? <>
      <button className="btn sm" disabled={importing} onClick={doImport}>{importing ? <Spinner /> : <Icon name="download" size={13} />}Import from Backrest</button>
      <button className="btn primary sm" onClick={showHooks}><Icon name="link" size={13} />Install hooks</button>
      {d.url && <a className="btn ghost sm" href={d.url.replace('127.0.0.1', location.hostname)} target="_blank" rel="noreferrer"><Icon name="external" size={13} />Open Backrest</a>}
    </> : undefined}>
      {!d ? <Loading /> : !d.configured ? (
        <Empty title="No Backrest on this box" icon="archive">
          Pocket Backups works on its own. If you already use <a href="https://github.com/garethgeorge/backrest" target="_blank" rel="noreferrer">Backrest</a>, set BACKREST_URL (and BACKREST_USER / BACKREST_PASSWORD) for Pocket Backups: its plans keep making the snapshots, and their hooks call this app for the dumps before and the copies, reports and notifications after.
        </Empty>
      ) : (
        <div className="stack loose">
          <p className="muted" style={{ margin: 0 }}>Backrest makes the snapshots on its schedules. Its hooks call Pocket Backups before each snapshot (database dumps, mounting a disk, the coverage check) and after it (retention, copies to the NAS and the clouds, the Homepage report, the heartbeat, notifications).</p>
          {d.error && <div className="callout danger"><Icon name="alert" size={15} /><span>Backrest does not answer: {d.error}</span></div>}
          {d.watch_error && <div className="callout warn"><Icon name="alert" size={15} /><span>Watching Backrest’s results failed: {d.watch_error}</span></div>}
          {d.plans.length === 0 && !d.error && <span className="muted">Backrest has no plans.</span>}
          {d.plans.map(p => {
            const job = jobs.find(j => j.id === p.job_id)
            return (
              <Card key={p.id} title={<span className="row"><Icon name="archive" size={16} />{p.id}</span>} sub={<>repository <code>{p.repo}</code> · {cronText(p.cron)}{p.next_run && ` · next ${friendlyTime(p.next_run, h12)} (${until(p.next_run)})`}</>}
                actions={<>{p.hooked ? <Badge lvl="ok" icon="link">Calls Pocket Backups</Badge> : <Badge lvl="warn" icon="alert">Not hooked</Badge>}</>}>
                <div className="stack tight">
                  <div className="row wrap">
                    <span className="muted small">Job:</span>
                    {job ? <Link to={`/jobs/${job.id}`}>{job.name}</Link> : <span className="muted small">none — Import from Backrest makes one</span>}
                    <span className="spacer" />
                    {p.last_backup && <span className="row small"><span className="muted">Last snapshot</span><OpBadge op={p.last_backup} />{friendlyTime(p.last_backup.end || p.last_backup.start, h12)}{p.last_backup.data_added && <span className="muted">+{fmtBytes(Number(p.last_backup.data_added))}</span>}{p.last_backup.errors > 0 && <span className="danger-text">{p.last_backup.errors} unreadable file(s)</span>}</span>}
                  </div>
                  <div className="path-chips">{p.paths.slice(0, 14).map(x => <code key={x}>{x}</code>)}{p.paths.length > 14 && <span className="muted xsmall">+{p.paths.length - 14} more</span>}{p.excludes > 0 && <span className="muted xsmall">· {p.excludes} exclude rule(s)</span>}</div>
                  {p.old_scripts.length > 0 && (
                    <details className="disclose">
                      <summary>{p.old_scripts.length} hook(s) still run other commands</summary>
                      <div className="stack tight mt-s">{p.old_scripts.map((s, i) => <code key={i} className="block xsmall wrap-any">{s}</code>)}</div>
                    </details>
                  )}
                  {p.recent.length > 0 && (
                    <details className="disclose">
                      <summary>Recent operations</summary>
                      <table className="table compact mt-s"><tbody>
                        {p.recent.map(o => <tr key={o.id}><td>{o.kind}</td><td><OpBadge op={o} /></td><td className="muted small">{friendlyTime(o.start, h12)}</td><td className="muted small ellipsis">{o.message || (o.snapshot ? o.snapshot.slice(0, 8) : '')}</td></tr>)}
                      </tbody></table>
                    </details>
                  )}
                </div>
              </Card>
            )
          })}
          {d.repos.length > 0 && (
            <Card title="Backrest repositories" flush>
              <div className="mini-list">{d.repos.map(r => (
                <div key={r.id} className="mini-row"><Icon name="hard-drive" size={14} className="muted" /><span className="grow min0"><span className="block">{r.id}</span><code className="xsmall muted ellipsis block">{r.uri}</code></span>
                  {r.repository_id ? <Link to={`/places/repositories/${r.repository_id}`} className="small">In Places</Link> : <span className="muted xsmall">not imported</span>}</div>
              ))}</div>
            </Card>
          )}
        </div>
      )}
      {preview && (
        <Modal title="Install hooks in Backrest" onClose={() => setPreview(null)} size="xl" footer={<>
          <button className="btn" onClick={() => setPreview(null)}>Cancel</button>
          <button className="btn primary" disabled={installing || preview.length === 0} onClick={install}>{installing && <Spinner />}Install</button>
        </>}>
          <div className="stack">
            <p className="muted small" style={{ margin: 0 }}>For every plan a job runs on (and its repository), the hooks are replaced with calls to Pocket Backups. A new API token named “Backrest hooks” is made for them. What changes:</p>
            {preview.length === 0 && <div className="callout warn"><Icon name="info" size={15} /><span>No plan has a job yet — use Import from Backrest first.</span></div>}
            {preview.map(c => (
              <div key={c.kind + c.id} className="hook-diff">
                <div className="row"><Badge lvl="info">{c.kind === 'plan' ? 'Plan' : 'Repository'}</Badge><b>{c.id}</b></div>
                <div className="two-col">
                  <div><div className="muted xsmall upper">Now</div>{c.before.length ? c.before.map((h, i) => <code key={i} className="block xsmall wrap-any old">{h}</code>) : <span className="muted xsmall">no hooks</span>}</div>
                  <div><div className="muted xsmall upper">After</div>{c.after.map((h, i) => <code key={i} className="block xsmall wrap-any new">{h}</code>)}</div>
                </div>
              </div>
            ))}
          </div>
        </Modal>
      )}
    </Shell>
  )
}
