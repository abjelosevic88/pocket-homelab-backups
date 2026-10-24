// Pick a snapshot, walk its folders, and get files back: download them, or
// put them back on the box.
import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { ApiError, api, qs, V1 } from '../lib/api'
import { useActions } from '../lib/actions'
import { useApp } from '../lib/app-context'
import { fmtBytes, friendlyTime } from '../lib/format'
import type { Repository, Run, SnapNode, Snapshot } from '../lib/types'
import { Icon } from './icons'
import { PathInput } from './pickers'
import { errMsg, Field, Loading, Modal, Segmented, Spinner, useToast } from './ui'

export function SnapshotBrowser({ repo }: { repo: Repository }) {
  const { h12, version } = useApp()
  const { repoAction } = useActions()
  const [snaps, setSnaps] = useState<Snapshot[] | null>(null)
  const [err, setErr] = useState<{ msg: string; mount: boolean } | null>(null)
  const [snap, setSnap] = useState<Snapshot | null>(null)
  const [busy, setBusy] = useState(false)
  const load = (fresh = false) => {
    setErr(null); setSnaps(null)
    api.get<Snapshot[]>(`${V1}/repositories/${repo.id}/snapshots${qs({ fresh })}`).then(setSnaps)
      .catch(e => setErr({ msg: errMsg(e), mount: e instanceof ApiError && e.status === 409 && repo.location === 'usb' }))
  }
  useEffect(() => { setSnap(null); load() }, [repo.id]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => { if (repo.disk_mounted && err?.mount) load() }, [version]) // eslint-disable-line react-hooks/exhaustive-deps

  if (err) return (
    <div className="callout warn">
      <Icon name="alert" size={15} />
      <span className="grow">{err.msg}</span>
      {err.mount && <button className="btn sm" disabled={busy} onClick={async () => { setBusy(true); await repoAction(repo, 'mount'); setBusy(false); load(true) }}>{busy ? <Spinner /> : <Icon name="plug" size={13} />}Mount the disk</button>}
      <button className="btn sm ghost" onClick={() => load(true)}>Try again</button>
    </div>
  )
  if (!snaps) return <Loading label="Reading the snapshots…" />
  if (snap) return <FileBrowser repo={repo} snap={snap} onBack={() => setSnap(null)} />
  if (!snaps.length) return <p className="muted">No snapshots in this repository yet.</p>
  return (
    <div className="snap-list">
      <div className="row muted small"><span className="grow">{snaps.length} snapshot(s), newest first. Pick the night to restore from.</span><button className="btn ghost sm" onClick={() => load(true)}><Icon name="refresh" size={13} />Refresh</button></div>
      {snaps.map(s => (
        <button key={s.id} type="button" className="snap-row" onClick={() => setSnap(s)}>
          <Icon name="archive" size={16} className="muted" />
          <span className="grow min0">
            <span className="snap-when">{friendlyTime(s.time, h12)}</span>
            <span className="muted xsmall ellipsis block">{s.hostname} · {s.paths.length} folder(s): {s.paths.slice(0, 3).join(', ')}{s.paths.length > 3 ? '…' : ''}</span>
          </span>
          {s.summary && <span className="muted xsmall tnum right">{fmtBytes(s.summary.total_bytes_processed)}<br />+{fmtBytes(s.summary.data_added_packed || s.summary.data_added)}</span>}
          {(s.tags || []).filter(t => !t.startsWith('job:')).slice(0, 2).map(t => <span key={t} className="tag">{t}</span>)}
          <code className="xsmall faint">{s.short_id}</code>
          <Icon name="chevron-right" size={14} className="faint" />
        </button>
      ))}
    </div>
  )
}

function FileBrowser({ repo, snap, onBack }: { repo: Repository; snap: Snapshot; onBack: () => void }) {
  const { h12 } = useApp()
  const { toast } = useToast()
  const [path, setPath] = useState('/')
  const [nodes, setNodes] = useState<SnapNode[] | null>(null)
  const [err, setErr] = useState('')
  const [sel, setSel] = useState<SnapNode[]>([])
  const [restore, setRestore] = useState(false)
  useEffect(() => {
    setNodes(null); setErr('')
    api.get<{ entries: SnapNode[] }>(`${V1}/repositories/${repo.id}/ls${qs({ snapshot: snap.id, path })}`).then(r => setNodes(r.entries)).catch(e => { setErr(errMsg(e)); setNodes([]) })
  }, [path, repo.id, snap.id])
  const parts = path.split('/').filter(Boolean)
  const toggle = (n: SnapNode) => setSel(s => s.some(x => x.path === n.path) ? s.filter(x => x.path !== n.path) : [...s, n])
  const dl = (n: SnapNode) => `${V1}/repositories/${repo.id}/download${qs({ snapshot: snap.id, path: n.path, zip: n.type === 'dir' })}`
  const download = () => {
    if (sel.length > 1) toast('Downloading each item separately', 'ok')
    for (const n of sel) window.open(dl(n), '_blank')
  }
  return (
    <div className="stack tight">
      <div className="row wrap">
        <button className="btn ghost sm" onClick={onBack}><Icon name="chevron-left" size={14} />Snapshots</button>
        <span className="muted small">{friendlyTime(snap.time, h12)} · <code>{snap.short_id}</code></span>
        <span className="spacer" />
        {sel.length > 0 && <>
          <span className="small">{sel.length} selected</span>
          <button className="btn sm" onClick={download}><Icon name="download" size={13} />Download</button>
          <button className="btn primary sm" onClick={() => setRestore(true)}><Icon name="rotate" size={13} />Put back on the box</button>
        </>}
      </div>
      <div className="crumbs">
        <button type="button" className="link-btn" onClick={() => setPath('/')}>/</button>
        {parts.map((p, i) => <span key={i} className="row" style={{ gap: 2 }}><Icon name="chevron-right" size={12} className="faint" /><button type="button" className="link-btn" onClick={() => setPath('/' + parts.slice(0, i + 1).join('/'))}>{p}</button></span>)}
      </div>
      {err && <div className="callout danger"><Icon name="alert" size={15} /><span>{err}</span></div>}
      {!nodes ? <Loading /> : (
        <div className="pick-list tall">
          {nodes.length === 0 && !err && <div className="muted small pick-empty">This folder is empty.</div>}
          {nodes.map(n => {
            const on = sel.some(x => x.path === n.path)
            return (
              <div key={n.path} className={`file-row ${on ? 'sel' : ''}`}>
                <input type="checkbox" checked={on} onChange={() => toggle(n)} aria-label={`Select ${n.name}`} />
                {n.type === 'dir'
                  ? <button type="button" className="file-name" onClick={() => setPath(n.path)}><Icon name="folder" size={15} className="folder-ico" /><span className="ellipsis">{n.name}</span></button>
                  : <a className="file-name" href={dl(n)} target="_blank" rel="noreferrer" title="Download"><Icon name="file" size={15} className="muted" /><span className="ellipsis">{n.name}</span></a>}
                <span className="muted xsmall tnum">{n.type === 'dir' ? '' : fmtBytes(n.size)}</span>
                <span className="muted xsmall nowrap">{n.mtime ? friendlyTime(n.mtime, h12) : ''}</span>
              </div>
            )
          })}
        </div>
      )}
      {restore && <RestoreDialog repo={repo} snap={snap} items={sel} onClose={() => setRestore(false)} />}
    </div>
  )
}

function RestoreDialog({ repo, snap, items, onClose }: { repo: Repository; snap: Snapshot; items: SnapNode[]; onClose: () => void }) {
  const { toast } = useToast()
  const navigate = useNavigate()
  const [where, setWhere] = useState<'original' | 'other'>('other')
  const [target, setTarget] = useState('')
  const [overwrite, setOverwrite] = useState('never')
  const [busy, setBusy] = useState(false)
  const go = async () => {
    setBusy(true)
    try {
      const r = await api.post<Run>(`${V1}/repositories/${repo.id}/restore`, { snapshot: snap.id, paths: items.map(i => i.path), target: where === 'original' ? '/' : target, overwrite })
      toast('Restoring…'); onClose(); navigate(`/runs/${r.id}`)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Put files back on the box" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={busy || (where === 'other' && !target.startsWith('/'))} onClick={go}>{busy && <Spinner />}Restore {items.length} item(s)</button>
    </>}>
      <div className="stack">
        <div className="muted small">{items.slice(0, 5).map(i => <div key={i.path} className="mono ellipsis">{i.path}</div>)}{items.length > 5 && <div>…and {items.length - 5} more</div>}</div>
        <Field label="Where to" group>
          <Segmented value={where} onChange={setWhere} label="Where" options={[{ value: 'other', label: 'Into another folder' }, { value: 'original', label: 'Back where they came from' }]} />
        </Field>
        {where === 'other'
          ? <Field label="Folder" help="The files keep their full path inside it, e.g. folder/home/me/file.txt — so nothing in use is replaced."><PathInput value={target} onChange={setTarget} placeholder="/home/me/restored" /></Field>
          : <div className="callout warn"><Icon name="alert" size={15} /><span>Files go back to their original place. Stop the app they belong to first if it is running.</span></div>}
        <Field label="When a file is already there" group>
          <Segmented size="sm" value={overwrite} onChange={setOverwrite} label="Overwrite" options={[{ value: 'never', label: 'Keep it' }, { value: 'if-newer', label: 'Replace if older' }, { value: 'if-changed', label: 'Replace if different' }, { value: 'always', label: 'Always replace' }]} />
        </Field>
      </div>
    </Modal>
  )
}
