import { useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { api, qs, V1 } from '../lib/api'
import { useActions } from '../lib/actions'
import { useApp } from '../lib/app-context'
import { useData } from '../lib/data'
import { cronText, fmtBytes, friendlyTime } from '../lib/format'
import type { Report, Repository, Run } from '../lib/types'
import { RunRow } from '../components/cards'
import { openRepoEditor } from '../components/dialogs'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { SnapshotBrowser } from '../components/SnapshotBrowser'
import { Badge } from '../components/Status'
import { Card, Confirm, CopyField, Empty, errMsg, Loading, MenuButton, Modal, Spinner, useAsync, useToast } from '../components/ui'

const LOC: Record<Repository['location'], string> = { local: 'A folder on this box', usb: 'A USB disk', connection: 'A connection', uri: 'A restic address' }

export default function RepoPage() {
  const { id } = useParams()
  const { repos, jobs, conns, loaded, reload } = useData()
  const { h12, version, bump } = useApp()
  const { repoAction } = useActions()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [pw, setPw] = useState<string | null>(null)
  const [askPw, setAskPw] = useState(false)
  const [del, setDel] = useState(false)
  const [report, setReport] = useState<Report | null>(null)
  const [reportBusy, setReportBusy] = useState(false)
  const repo = repos.find(r => String(r.id) === id)
  const runs = useAsync(() => api.get<Run[]>(`${V1}/runs${qs({ repository: id, limit: 15 })}`), [id, version])
  if (!loaded) return <Shell title="Repository"><Loading /></Shell>
  if (!repo) return <Shell title="Repository"><Empty title="This repository is gone" icon="hard-drive"><Link to="/places">Places</Link></Empty></Shell>

  const st = repo.stats
  const conn = conns.find(c => c.id === repo.connection_id)
  const users = jobs.filter(j => repo.jobs.includes(j.id))
  const pocket = repo.managed_by === 'pocket'
  const usb = repo.location === 'usb'

  const show = async () => {
    try { const r = await api.post<{ password: string }>(`${V1}/repositories/${repo.id}/password`); setPw(r.password); setAskPw(false) } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async () => {
    try { await api.del(`${V1}/repositories/${repo.id}`); toast('Forgotten — the backups themselves were not touched'); await reload(); bump(); navigate('/places') } catch (e) { toast(errMsg(e), 'err'); setDel(false) }
  }
  const loadReport = async (fresh = false) => {
    setReportBusy(true)
    try { setReport(await api.get<Report>(`${V1}/repositories/${repo.id}/report${qs({ fresh })}`)) } catch (e) { toast(errMsg(e), 'err') } finally { setReportBusy(false) }
  }
  const act = async (a: string) => { const r = await repoAction(repo, a); if (r && 'id' in r) navigate(`/runs/${r.id}`) }

  return (
    <Shell title={repo.name} sub={LOC[repo.location]} controls={<>
      {usb && (repo.disk_mounted
        ? <button className="btn sm" onClick={() => act('unmount')}><Icon name="power" size={13} />Unmount</button>
        : <button className="btn sm" disabled={!repo.disk_connected} title={repo.disk_connected ? '' : 'Plug the disk in first'} onClick={() => act('mount')}><Icon name="plug" size={13} />Mount</button>)}
      <button className="btn sm" onClick={() => act('check')}><Icon name="shield-check" size={13} />Check now</button>
      <button className="btn sm" onClick={() => openRepoEditor(repo)}><Icon name="edit" size={13} />Edit</button>
      <MenuButton label="More" align="right" items={[
        { label: 'Read sizes again', icon: 'refresh', onSelect: () => act('stats') },
        { label: 'Check every byte', icon: 'shield-check', onSelect: async () => { const r = await repoAction(repo, 'check', 100); if (r && 'id' in r) navigate(`/runs/${r.id}`) } },
        { label: 'Prune (free space)', icon: 'trash', onSelect: () => act('prune') },
        { label: 'Remove stale locks', icon: 'lock', onSelect: () => act('unlock') },
        repo.copies_paused ? { label: 'Resume the copies', icon: 'play', onSelect: () => act('resume') } : { label: 'Pause the copies', icon: 'pause', onSelect: () => act('pause') },
        { label: 'Show the password', icon: 'key', onSelect: () => setAskPw(true) },
        'sep', { label: 'Forget this repository', icon: 'trash', danger: true, onSelect: () => setDel(true) },
      ]}><Icon name="more" size={16} /></MenuButton>
    </>}>
      <div className="stack loose">
        {repo.copies_paused && (
          <div className="callout danger"><Icon name="pause" size={15} /><span className="grow"><b>Copies of this repository are paused.</b> {repo.paused_reason} — so a damaged repository cannot overwrite the good copies elsewhere. Resume them once it is repaired.</span>
            <button className="btn sm" onClick={() => act('resume')}>Resume copies</button></div>
        )}
        {repo.status === 'error' && <div className="callout warn"><Icon name="alert" size={15} /><span>It did not open last time: {repo.last_error}</span></div>}

        <div className="stat-row">
          <div className="stat"><span className="stat-n tnum">{st.updated_at ? fmtBytes(st.size) : '–'}</span><span className="stat-l">stored</span></div>
          <div className="stat"><span className="stat-n tnum">{st.updated_at ? st.snapshots : '–'}</span><span className="stat-l">snapshots</span></div>
          <div className="stat"><span className="stat-n">{st.latest ? friendlyTime(st.latest, h12) : '–'}</span><span className="stat-l">newest</span></div>
          {st.disk_used_pct ? <div className="stat"><span className="stat-n tnum">{st.disk_used_pct}%</span><span className="stat-l">disk full · {fmtBytes(st.disk_free || 0)} free</span></div> : null}
        </div>

        <div className="two-col top">
          <Card title="Details">
            <dl className="kv">
              <div><dt>Lives</dt><dd>{LOC[repo.location]}{conn ? <> — <Link to="/places">{conn.name}</Link></> : null}</dd></div>
              <div><dt>Address</dt><dd><code>{repo.uri || repo.path}</code></dd></div>
              {usb && repo.options.usb && <div><dt>Disk</dt><dd>
                <span className="row wrap">{repo.disk_connected ? <Badge lvl={repo.disk_mounted ? 'run' : 'ok'} icon="plug">{repo.disk_mounted ? 'Plugged in and mounted' : 'Plugged in'}</Badge> : <Badge lvl="off" icon="usb">Unplugged</Badge>}
                  <code className="xsmall">{repo.options.usb.label || repo.options.usb.uuid}</code> at <code className="xsmall">{repo.options.usb.mountpoint}</code></span></dd></div>}
              <div><dt>Password</dt><dd>{repo.password_file ? <>read from <code>{repo.password_file}</code></> : repo.has_password ? 'Kept sealed in Pocket Backups' : <span className="danger-text">none</span>}</dd></div>
              <div><dt>Compression</dt><dd>{repo.options.compression === 'max' ? 'Smallest' : repo.options.compression === 'off' ? 'Off' : 'Normal'}{repo.options.background ? ' · in the background' : ''}</dd></div>
              <div><dt>Checks</dt><dd>{pocket ? <>{cronText(repo.check_schedule).replace('Only when started by hand', 'Never')}{repo.check_read_pct ? `, reading ${repo.check_read_pct}% of the data` : ''}</> : 'Run by Backrest'}</dd></div>
              {pocket && <div><dt>Prune</dt><dd>{repo.prune_schedule ? cronText(repo.prune_schedule) : 'After runs only (job retention)'}</dd></div>}
              {repo.backrest_repo && <div><dt>Backrest</dt><dd>repository <code>{repo.backrest_repo}</code></dd></div>}
            </dl>
          </Card>
          <Card title="Used by">
            {users.length === 0 ? <span className="muted small">No job uses it yet.</span> : (
              <div className="mini-list">{users.map(j => <Link key={j.id} to={`/jobs/${j.id}`} className="mini-row"><Icon name="briefcase" size={14} className="muted" /><span className="grow">{j.name}</span>{j.repository_id !== repo.id && <span className="muted xsmall">as a copy</span>}</Link>)}</div>
            )}
          </Card>
        </div>

        <Card title="What takes the space" sub="Size of each app in the newest snapshot." actions={<button className="btn ghost sm" disabled={reportBusy} onClick={() => loadReport(!!report)}>{reportBusy ? <Spinner /> : <Icon name="refresh" size={13} />}{report ? 'Again' : 'Work it out'}</button>}>
          {!report ? <span className="muted small">Reads the newest snapshot’s file list — takes a moment on big repositories.</span> : (
            <div className="bars">
              <div className="muted xsmall">{report.snapshots} snapshot(s) · repository {fmtBytes(report.repository_size)}</div>
              {report.rows.slice(0, 30).map(r => (
                <div key={r.name} className="bar-row"><span className="ellipsis">{r.name}</span><span className="bar-track"><span style={{ width: `${Math.max(0.8, r.bytes / (report.rows[0]?.bytes || 1) * 100)}%` }} /></span><span className="tnum muted xsmall right">{fmtBytes(r.bytes)}</span></div>
              ))}
              {report.rows.length > 30 && <div className="muted xsmall">…and {report.rows.length - 30} more</div>}
            </div>
          )}
        </Card>

        <Card title="Snapshots" sub="Pick one to look inside and get files back."><SnapshotBrowser repo={repo} /></Card>

        <Card title="Runs" flush>
          {!runs.data ? <Loading /> : runs.data.length === 0 ? <Empty title="No runs yet" icon="history" /> : <div className="run-list">{runs.data.map(r => <RunRow key={r.id} run={r} />)}</div>}
        </Card>
      </div>
      {askPw && <Confirm title="Show the password?" danger={false} confirmLabel="Show it" onCancel={() => setAskPw(false)} onConfirm={show}
        message="Anyone with this password and a copy of the repository can read every backed-up file. Show it only to store it somewhere safe (a password manager)." />}
      {pw !== null && <Modal title="Repository password" onClose={() => setPw(null)} size="sm"><div className="stack tight"><CopyField value={pw} secret label="Password" /><span className="muted small">Without it these backups cannot be restored — keep a copy outside this box.</span></div></Modal>}
      {del && <Confirm title={`Forget ${repo.name}?`} message="Pocket Backups stops using it. The repository and its snapshots stay where they are and can be added again later. Jobs that use it must change first." onConfirm={remove} onCancel={() => setDel(false)} confirmLabel="Forget" />}
    </Shell>
  )
}
