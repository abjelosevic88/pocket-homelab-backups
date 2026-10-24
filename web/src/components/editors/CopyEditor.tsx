import { useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { useData } from '../../lib/data'
import type { Copy, CopyOptions, Job } from '../../lib/types'
import { Icon } from '../icons'
import { NumInput, PathInput } from '../pickers'
import { Select } from '../Select'
import { Confirm, errMsg, Field, Modal, Segmented, Spinner, useToast } from '../ui'
import { Check, FormSection } from './common'

export function CopyEditor({ job, copy, onClose }: { job: Job; copy?: Copy; onClose: () => void }) {
  const { conns, repos, reload } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const [name, setName] = useState(copy?.name || '')
  const [mode, setMode] = useState<Copy['mode']>(copy?.mode || 'mirror')
  const [to, setTo] = useState<'connection' | 'folder'>(copy && !copy.connection_id ? 'folder' : 'connection')
  const [conn, setConn] = useState<number | null>(copy?.connection_id ?? conns[0]?.id ?? null)
  const [path, setPath] = useState(copy?.path || '')
  const [target, setTarget] = useState<number | null>(copy?.target_repository_id ?? null)
  const [enabled, setEnabled] = useState(copy?.enabled ?? true)
  const [o, setO] = useState<CopyOptions>(() => ({ ...(copy?.options || {}) }))
  const [busy, setBusy] = useState(false)
  const [del, setDel] = useState(false)
  const kindOf = conns.find(c => c.id === conn)?.kind

  const save = async () => {
    setBusy(true)
    try {
      const body = { name: name.trim(), mode, connection_id: mode === 'mirror' && to === 'connection' ? conn : null, path: mode === 'mirror' ? path.trim() : '',
        target_repository_id: mode === 'restic' ? target : null, enabled, options: o }
      if (copy) await api.put(`${V1}/jobs/${job.id}/copies/${copy.id}`, body)
      else await api.post(`${V1}/jobs/${job.id}/copies`, body)
      toast(copy ? 'Copy saved' : 'Copy added')
      await reload(); bump()
      onClose()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const remove = async () => {
    try { await api.del(`${V1}/jobs/${job.id}/copies/${copy!.id}`); toast('Copy removed — the copied data was not touched'); await reload(); bump(); onClose() } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <Modal title={copy ? `Edit ${copy.name}` : `Copy ${job.name} somewhere else`} onClose={onClose} size="lg" footer={<>
      {copy && <button className="btn danger-ghost" onClick={() => setDel(true)}><Icon name="trash" size={14} />Remove</button>}
      <span className="spacer" />
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={busy || !name.trim()} onClick={save}>{busy && <Spinner />}{copy ? 'Save' : 'Add copy'}</button>
    </>}>
      <div className="stack">
        <p className="muted small" style={{ margin: 0 }}>After every good snapshot the repository is copied here too — a second (or third) home for the backups, ideally in another building.</p>
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} placeholder="NAS, Google Drive, B2…" autoFocus /></Field>
        <Field label="How" group>
          <Segmented value={mode} onChange={setMode} label="Mode" options={[{ value: 'mirror', label: 'An exact copy of the repository' }, { value: 'restic', label: 'Its snapshots into another repository' }]} />
        </Field>
        {mode === 'mirror' ? (
          <FormSection title="Where to" sub="Files that disappear from the repository (forgotten snapshots) disappear there too, so it never grows past the original.">
            <Segmented size="sm" value={to} onChange={setTo} label="Target" options={[{ value: 'connection', label: 'A connection' }, { value: 'folder', label: 'A folder on this box' }]} />
            {to === 'connection' ? <>
              <Select value={conn ?? 0} onChange={v => setConn(v || null)} placeholder="Choose a connection…" options={conns.map(c => ({ value: c.id, label: c.name, hint: c.kind }))} />
              <Field label="Folder in the connection"><PathInput value={path} onChange={setPath} connection={conn} placeholder="Backups/restic-repo" /></Field>
            </> : <Field label="Folder"><PathInput value={path} onChange={setPath} placeholder="/mnt/other-disk/restic-repo" /></Field>}
            <div className="row wrap">
              <Field label="Files at once"><NumInput value={o.transfers} onChange={v => setO({ ...o, transfers: v })} placeholder="4" /></Field>
              <Field label="Speed limit" help="e.g. 10M (per second)"><input className="mono" style={{ width: 120 }} value={o.bwlimit || ''} onChange={e => setO({ ...o, bwlimit: e.target.value })} /></Field>
            </div>
            <Check checked={o.delete_before} onChange={v => setO({ ...o, delete_before: v })} label="Delete old files first" help="Frees space before uploading — needed when a cloud quota is nearly full." />
            {kindOf === 'b2' && <Check checked={o.hard_delete} onChange={v => setO({ ...o, hard_delete: v })} label="Really delete on B2" help="Otherwise B2 keeps every old file as a hidden version and the bucket keeps growing." />}
            {kindOf === 'gdrive' && <Check checked={o.use_trash} onChange={v => setO({ ...o, use_trash: v })} label="Send removed files to the Drive trash" help="Off frees the space at once." />}
          </FormSection>
        ) : (
          <FormSection title="Into which repository" sub="restic copy adds the new snapshots there; that repository keeps its own history and retention. Good for an offline USB disk.">
            <Select value={target ?? 0} onChange={v => setTarget(v || null)} placeholder="Choose a repository…" options={repos.filter(r => r.id !== job.repository_id).map(r => ({ value: r.id, label: r.name, hint: r.location }))} />
          </FormSection>
        )}
        <Check checked={enabled} onChange={setEnabled} label="Switched on" />
      </div>
      {del && <Confirm title={`Remove ${copy?.name}?`} message="Future runs stop copying there. What was already copied stays where it is." onConfirm={remove} onCancel={() => setDel(false)} confirmLabel="Remove" />}
    </Modal>
  )
}
