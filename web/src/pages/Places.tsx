import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useData } from '../lib/data'
import { ago, fmtBytes } from '../lib/format'
import type { About, Connection, Importable } from '../lib/types'
import { RepoCard } from '../components/cards'
import { openConnEditor, openRepoEditor } from '../components/dialogs'
import { Icon } from '../components/icons'
import { FolderPicker } from '../components/pickers'
import { Shell } from '../components/Shell'
import { Badge } from '../components/Status'
import { Confirm, Empty, errMsg, Loading, MenuButton, Modal, Spinner, useToast } from '../components/ui'
import { CONN_ICON, CONN_NAME } from './connKinds'

function connDetail(c: Connection): string {
  const x = c.config
  switch (c.kind) {
    case 'sftp': return `${x.user ? x.user + '@' : ''}${x.host || ''}${x.root ? ':' + x.root : ''}`
    case 'webdav': return x.url || ''
    case 'b2': case 's3': return [x.bucket, x.endpoint].filter(Boolean).join(' · ')
    case 'gdrive': return x.scope === 'drive.readonly' ? 'read only' : x.scope === 'drive.file' ? 'only its own files' : 'all files'
    case 'pocketfiles': return x.account ? `as ${x.account}` : ''
    default: return x.account || ''
  }
}

export default function Places() {
  const { repos, conns, loaded, reload } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const [params, setParams] = useSearchParams()
  const [hl, setHl] = useState<number | null>(null)
  const [testing, setTesting] = useState<number | null>(null)
  const [about, setAbout] = useState<Record<number, About>>({})
  const [browse, setBrowse] = useState<Connection | null>(null)
  const [del, setDel] = useState<Connection | null>(null)
  const [imp, setImp] = useState(false)

  useEffect(() => {
    const err = params.get('error'), ok = params.get('connected')
    if (err) toast(err, 'err')
    if (ok) { toast('Connected'); setHl(Number(ok)); setTimeout(() => setHl(null), 4000); reload() }
    if (err || ok) setParams({}, { replace: true })
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  const test = async (c: Connection) => {
    setTesting(c.id)
    try {
      const r = await api.post<{ ok: boolean; error?: string; about?: About }>(`${V1}/connections/${c.id}/test`)
      if (r.ok) { toast(`${c.name} answers`); if (r.about) setAbout(a => ({ ...a, [c.id]: r.about! })) } else toast(r.error || 'It does not answer', 'err')
      await reload(); bump()
    } catch (e) { toast(errMsg(e), 'err') } finally { setTesting(null) }
  }
  const remove = async () => {
    try { await api.del(`${V1}/connections/${del!.id}`); toast('Connection removed'); setDel(null); await reload(); bump() } catch (e) { toast(errMsg(e), 'err'); setDel(null) }
  }

  return (
    <Shell title="Places" controls={<>
      <button className="btn sm" onClick={() => openConnEditor()}><Icon name="cloud" size={13} />Add a connection</button>
      <button className="btn primary sm" onClick={() => openRepoEditor()}><Icon name="plus" size={13} />Add a repository</button>
    </>}>
      {!loaded ? <Loading /> : (
        <div className="stack loose">
          <section>
            <div className="sec-head"><h2>Repositories</h2><span className="muted small">Where snapshots are kept</span></div>
            {repos.length === 0 ? <Empty title="No repository yet" icon="hard-drive">A repository keeps your snapshots, encrypted. <button className="link-btn" onClick={() => openRepoEditor()}>Add one</button> — on this box, a USB disk, a NAS or a cloud.</Empty>
              : <div className="card-grid">{repos.map(r => <RepoCard key={r.id} repo={r} />)}</div>}
          </section>

          <section>
            <div className="sec-head"><h2>Connections</h2><span className="muted small">Clouds and servers outside this box</span><span className="spacer" /><button className="btn ghost sm" onClick={() => setImp(true)}><Icon name="download" size={13} />Import from rclone.conf</button></div>
            {conns.length === 0 ? <Empty title="No connections yet" icon="cloud">Connect Google Drive, OneDrive, Dropbox, Backblaze B2, S3, a NAS over SFTP, WebDAV or Pocket Files to keep backups there. <button className="link-btn" onClick={() => openConnEditor()}>Add a connection</button></Empty> : (
              <div className="card-grid">
                {conns.map(c => (
                  <div key={c.id} className={`conn-card ${hl === c.id ? 'hl' : ''}`}>
                    <div className="row">
                      <span className="repo-ico"><Icon name={CONN_ICON[c.kind]} size={17} /></span>
                      <span className="grow min0"><span className="repo-name ellipsis block">{c.name}</span><span className="muted xsmall">{CONN_NAME[c.kind]}</span></span>
                      <MenuButton label={`Options for ${c.name}`} align="right" className="btn ghost icon-only sm" items={[
                        { label: 'Test', icon: 'refresh', onSelect: () => test(c) },
                        { label: 'Browse', icon: 'folder', onSelect: () => setBrowse(c) },
                        { label: 'Edit', icon: 'edit', onSelect: () => openConnEditor(c) },
                        'sep', { label: 'Remove', icon: 'trash', danger: true, onSelect: () => setDel(c) },
                      ]}><Icon name="more" size={15} /></MenuButton>
                    </div>
                    <div className="mono xsmall muted ellipsis">{connDetail(c) || ' '}</div>
                    <div className="row wrap">
                      {c.status === 'ok' ? <Badge lvl="ok" icon="check-circle">Answers</Badge> : c.status === 'error' ? <Badge lvl="crit" icon="x-circle" title={c.last_error}>Does not answer</Badge> : <Badge lvl="off">Not tested</Badge>}
                      {c.checked_at && <span className="muted xsmall">checked {ago(c.checked_at)}</span>}
                      {about[c.id] && <span className="muted xsmall">{fmtBytes(about[c.id].used)} used{about[c.id].total ? ` of ${fmtBytes(about[c.id].total)}` : ''}</span>}
                      <span className="spacer" />
                      <button className="btn sm" disabled={testing === c.id} onClick={() => test(c)}>{testing === c.id ? <Spinner /> : <Icon name="refresh" size={13} />}Test</button>
                    </div>
                    {c.status === 'error' && <div className="xsmall danger-text clamp2">{c.last_error}</div>}
                  </div>
                ))}
              </div>
            )}
          </section>
        </div>
      )}
      {browse && <FolderPicker connection={browse.id} files onPick={() => setBrowse(null)} onClose={() => setBrowse(null)} />}
      {del && <Confirm title={`Remove ${del.name}?`} message="Only the connection is removed — nothing in the cloud or on the server is deleted. Repositories and copies that use it must go first." onConfirm={remove} onCancel={() => setDel(null)} confirmLabel="Remove" />}
      {imp && <ImportDialog onClose={() => setImp(false)} />}
    </Shell>
  )
}

function ImportDialog({ onClose }: { onClose: () => void }) {
  const { reload } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const [d, setD] = useState<Importable | null>(null)
  const [pick, setPick] = useState<string[]>([])
  const [busy, setBusy] = useState(false)
  useEffect(() => { api.get<Importable>(`${V1}/connections/importable`).then(setD).catch(e => { toast(errMsg(e), 'err'); onClose() }) }, []) // eslint-disable-line react-hooks/exhaustive-deps
  const go = async () => {
    setBusy(true)
    try { const r = await api.post<Connection[]>(`${V1}/connections/import`, { names: pick }); toast(`Imported ${r.length} connection(s)`); await reload(); bump(); onClose() } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title="Import from rclone.conf" onClose={onClose} footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={busy || !pick.length} onClick={go}>{busy && <Spinner />}Import {pick.length || ''}</button>
    </>}>
      {!d ? <Loading /> : !d.file ? (
        <p className="muted">No rclone.conf is offered on this box. Set RCLONE_IMPORT to the file (and mount it into the container) to import remotes you already use.</p>
      ) : (
        <div className="stack tight">
          <p className="muted small" style={{ margin: 0 }}>From <code>{d.file}</code>. Sign-ins, keys and passwords are copied and sealed; the file itself is not changed.</p>
          {d.remotes.length === 0 && <span className="muted">The file has no remotes.</span>}
          {d.remotes.map(r => (
            <label key={r.name} className={`multi-row ${r.imported ? 'dim' : ''}`}>
              <input type="checkbox" disabled={r.imported} checked={pick.includes(r.name)} onChange={e => setPick(p => e.target.checked ? [...p, r.name] : p.filter(x => x !== r.name))} />
              <Icon name={CONN_ICON[r.kind as keyof typeof CONN_ICON] || 'terminal'} size={14} />
              <span className="grow mono">{r.name}</span>
              <span className="muted small">{r.type}{r.scope ? ` · ${r.scope}` : ''}</span>
              {r.imported && <Badge lvl="ok">Imported</Badge>}
            </label>
          ))}
        </div>
      )}
    </Modal>
  )
}
