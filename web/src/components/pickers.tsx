// Inputs the editors share: one-per-line lists, numbers, and folder pickers
// for this box and for a connection.
import { useEffect, useState } from 'react'
import { api, qs, V1 } from '../lib/api'
import { fmtBytes } from '../lib/format'
import type { LocalEntry, RemoteEntry } from '../lib/types'
import { Icon } from './icons'
import { errMsg, Loading, Modal } from './ui'

/** A string list edited as one item per line. */
export function ListInput({ value, onChange, placeholder, rows = 3, mono = true }: { value?: string[] | null; onChange: (v: string[]) => void; placeholder?: string; rows?: number; mono?: boolean }) {
  const [text, setText] = useState((value || []).join('\n'))
  useEffect(() => {
    const cur = text.split('\n').map(s => s.trim()).filter(Boolean)
    if (cur.join('\n') !== (value || []).join('\n')) setText((value || []).join('\n'))
  }, [value]) // eslint-disable-line react-hooks/exhaustive-deps
  return <textarea className={mono ? 'mono' : ''} rows={rows} value={text} placeholder={placeholder} spellCheck={false}
    onChange={e => { setText(e.target.value); onChange(e.target.value.split('\n').map(s => s.trim()).filter(Boolean)) }} />
}

/** A number input that gives undefined for empty. */
export function NumInput({ value, onChange, min = 0, max, placeholder, width = 90 }: { value?: number | null; onChange: (v: number | undefined) => void; min?: number; max?: number; placeholder?: string; width?: number }) {
  return <input type="number" inputMode="numeric" min={min} max={max} style={{ width }} value={value ?? ''} placeholder={placeholder}
    onChange={e => onChange(e.target.value === '' ? undefined : Number(e.target.value))} />
}

/** A text input with a Browse button that opens a folder picker. */
export function PathInput({ value, onChange, placeholder, connection, files = false }: { value: string; onChange: (v: string) => void; placeholder?: string; connection?: number | null; files?: boolean }) {
  const [open, setOpen] = useState(false)
  return (
    <div className="path-input">
      <input className="mono" value={value} onChange={e => onChange(e.target.value)} placeholder={placeholder} spellCheck={false} autoComplete="off" />
      <button type="button" className="btn" onClick={() => setOpen(true)}><Icon name="folder" size={14} />Browse</button>
      {open && <FolderPicker start={value} connection={connection} files={files} onPick={p => { onChange(p); setOpen(false) }} onClose={() => setOpen(false)} />}
    </div>
  )
}

type Entry = { name: string; path: string; is_dir: boolean; size: number; mount?: boolean }

/** Browse folders on this box (connection unset) or of a connection. */
export function FolderPicker({ start, connection, files, onPick, onClose }: { start?: string; connection?: number | null; files?: boolean; onPick: (p: string) => void; onClose: () => void }) {
  const remote = !!connection
  const [path, setPath] = useState(() => remote ? (start || '') : (start && start.startsWith('/') ? start : '/'))
  const [entries, setEntries] = useState<Entry[] | null>(null)
  const [err, setErr] = useState('')
  const [root, setRoot] = useState('/')
  useEffect(() => {
    setEntries(null); setErr('')
    const url = remote ? `${V1}/connections/${connection}/browse${qs({ path, dirs: !files })}` : `${V1}/browse${qs({ path, dirs: !files })}`
    api.get<{ path: string; root?: string; entries: (LocalEntry | RemoteEntry)[] }>(url)
      .then(r => {
        if (r.root) setRoot(r.root)
        if (!remote && r.path !== path) setPath(r.path)
        setEntries(r.entries.map(e => ({ name: e.name, path: remote ? joinRemote(path, e.name) : (e as LocalEntry).path, is_dir: e.is_dir, size: e.size, mount: (e as LocalEntry).mount })))
      })
      .catch(e => {
        // a starting point that does not exist: go up one level
        if (!remote && path !== '/' && path !== root) { setPath(parentOf(path)); return }
        setErr(errMsg(e)); setEntries([])
      })
  }, [path, connection]) // eslint-disable-line react-hooks/exhaustive-deps
  const crumbs = remote ? ['', ...path.split('/').filter(Boolean)] : ['/', ...path.split('/').filter(Boolean)]
  const go = (i: number) => {
    if (remote) setPath(crumbs.slice(1, i + 1).join('/'))
    else setPath(i === 0 ? '/' : '/' + crumbs.slice(1, i + 1).join('/'))
  }
  return (
    <Modal title={remote ? 'Choose a folder in the connection' : 'Choose a folder on this box'} onClose={onClose} size="lg" footer={<>
      <span className="grow mono small ellipsis muted">{remote ? (path || '(top)') : path}</span>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" onClick={() => onPick(path)}>Use this folder</button>
    </>}>
      <div className="crumbs">
        {crumbs.map((c, i) => <span key={i} className="row" style={{ gap: 2 }}>{i > 0 && <Icon name="chevron-right" size={12} className="faint" />}<button type="button" className="link-btn" onClick={() => go(i)}>{c === '' ? 'Top' : c === '/' ? '/' : c}</button></span>)}
      </div>
      {err && <div className="callout danger mt-s"><Icon name="alert" size={15} /><span>{err}</span></div>}
      {!entries ? <Loading /> : (
        <div className="pick-list">
          {entries.length === 0 && !err && <div className="muted small pick-empty">This folder is empty.</div>}
          {entries.map(e => (
            <button type="button" key={e.path} className="pick-row" onClick={() => e.is_dir ? setPath(e.path) : onPick(e.path)}>
              <Icon name={e.is_dir ? 'folder' : 'file'} size={15} className={e.is_dir ? 'folder-ico' : 'muted'} />
              <span className="grow ellipsis">{e.name}</span>
              {e.mount && <span className="lvl-badge sm off" title="Something is mounted here (a disk or a share)"><Icon name="hard-drive" size={10} />mount</span>}
              {!e.is_dir && <span className="muted small tnum">{fmtBytes(e.size)}</span>}
              {e.is_dir && <Icon name="chevron-right" size={14} className="faint" />}
            </button>
          ))}
        </div>
      )}
    </Modal>
  )
}

const joinRemote = (a: string, b: string) => (a ? a.replace(/\/+$/, '') + '/' : '') + b
const parentOf = (p: string) => p.replace(/\/[^/]+\/?$/, '') || '/'
