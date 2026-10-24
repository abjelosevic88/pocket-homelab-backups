import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { useData } from '../../lib/data'
import type { RepoOptions, Repository, Run, USBDisk } from '../../lib/types'
import { Icon } from '../icons'
import { NumInput, PathInput } from '../pickers'
import { Select } from '../Select'
import { errMsg, Field, Modal, Segmented, Spinner, useToast } from '../ui'
import { Check, FormSection, ScheduleInput } from './common'

type PwMode = 'create' | 'password' | 'file' | 'keep'

export function RepoEditor({ repo, onClose }: { repo?: Repository; onClose: () => void }) {
  const { conns, reload } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [name, setName] = useState(repo?.name || '')
  const [location, setLocation] = useState<Repository['location']>(repo?.location || 'local')
  const [path, setPath] = useState(repo?.path || '')
  const [conn, setConn] = useState<number | null>(repo?.connection_id ?? conns[0]?.id ?? null)
  const [opts, setOpts] = useState<RepoOptions>(() => ({ ...(repo?.options || { compression: 'auto' }) }))
  const [usb, setUsb] = useState<USBDisk>(() => repo?.options.usb || { uuid: '', label: '', mountpoint: '/mnt/usbbackup', power_off: true, warn_pct: 85 })
  const [pwMode, setPwMode] = useState<PwMode>(repo ? 'keep' : 'create')
  const [password, setPassword] = useState('')
  const [pwFile, setPwFile] = useState(repo?.password_file || '')
  const [check, setCheck] = useState(repo?.check_schedule ?? '0 2 * * 0')
  const [checkPct, setCheckPct] = useState<number | undefined>(repo?.check_read_pct ?? 5)
  const [prune, setPrune] = useState(repo?.prune_schedule ?? '')
  const [busy, setBusy] = useState(false)
  const managed = (repo?.managed_by || 'pocket') === 'pocket'

  const save = async () => {
    setBusy(true)
    try {
      const body: Record<string, unknown> = {
        name: name.trim(), location, path: path.trim(), connection_id: location === 'connection' ? conn : null,
        options: { ...opts, usb: location === 'usb' ? usb : undefined },
      }
      if (managed) Object.assign(body, { check_schedule: check, check_read_pct: checkPct ?? 0, prune_schedule: prune })
      if (pwMode === 'create') body.create = true
      if (pwMode === 'password') body.password = password
      if (pwMode === 'file') body.password_file = pwFile.trim()
      const res = repo ? await api.put<{ repository: Repository; run?: Run }>(`${V1}/repositories/${repo.id}`, body)
        : await api.post<{ repository: Repository; run?: Run }>(`${V1}/repositories`, body)
      toast(res.run ? 'Creating the repository…' : repo ? 'Repository saved' : 'Repository added')
      await reload(); bump()
      onClose()
      if (res.run) navigate(`/runs/${res.run.id}`)
      else if (!repo) navigate(`/places/repositories/${res.repository.id}`)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }

  return (
    <Modal title={repo ? `Edit ${repo.name}` : 'Add a repository'} onClose={onClose} size="lg" footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={busy || !name.trim()} onClick={save}>{busy && <Spinner />}{repo ? 'Save' : pwMode === 'create' ? 'Create' : 'Add'}</button>
    </>}>
      <div className="stack">
        <p className="muted small" style={{ margin: 0 }}>A repository is where snapshots are kept — encrypted, deduplicated, each one a full picture of that night. It can live on this box, on a USB disk, on a NAS or in a cloud.</p>
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} placeholder="Nightly on the SSD" autoFocus /></Field>
        <Field label="Where it lives" group>
          <Segmented value={location} onChange={setLocation} label="Location" options={[
            { value: 'local', label: <span className="row"><Icon name="folder" size={14} />This box</span> },
            { value: 'usb', label: <span className="row"><Icon name="usb" size={14} />USB disk</span> },
            { value: 'connection', label: <span className="row"><Icon name="cloud" size={14} />A connection</span> },
            { value: 'uri', label: 'restic address' },
          ]} />
        </Field>
        {location === 'local' && <Field label="Folder"><PathInput value={path} onChange={setPath} placeholder="/home/me/backups/restic-repo" /></Field>}
        {location === 'connection' && <>
          <Field label="Connection">
            <Select value={conn ?? 0} onChange={v => setConn(v || null)} placeholder="Choose a connection…" options={conns.map(c => ({ value: c.id, label: c.name, hint: c.kind }))} />
          </Field>
          {!conns.length && <span className="muted small">Add a connection (Google Drive, a NAS over SFTP, B2…) first.</span>}
          <Field label="Folder in the connection"><PathInput value={path} onChange={setPath} connection={conn} placeholder="Backups/restic-repo" /></Field>
        </>}
        {location === 'uri' && <Field label="restic repository" help="Anything restic accepts: sftp:user@host:/path, rest:https://…, s3:…, b2:bucket:path."><input className="mono" value={path} onChange={e => setPath(e.target.value)} placeholder="sftp:me@nas:/mnt/tank/backups/repo" /></Field>}
        {location === 'usb' && (
          <FormSection title="The disk" sub="Mounted for each use and unmounted (and powered down) afterwards, so it stays offline between runs.">
            <div className="two-col">
              <Field label="Filesystem UUID" help="sudo blkid shows it."><input className="mono" value={usb.uuid} onChange={e => setUsb({ ...usb, uuid: e.target.value.trim() })} placeholder="93c05053-…" /></Field>
              <Field label="Label" help="Checked before writing, so it never writes to the wrong disk."><input className="mono" value={usb.label || ''} onChange={e => setUsb({ ...usb, label: e.target.value })} placeholder="usbbackup" /></Field>
              <Field label="Mount point"><input className="mono" value={usb.mountpoint} onChange={e => setUsb({ ...usb, mountpoint: e.target.value })} placeholder="/mnt/usbbackup" /></Field>
              <Field label="Warn when fuller than (%)"><NumInput value={usb.warn_pct} onChange={v => setUsb({ ...usb, warn_pct: v })} max={100} placeholder="85" /></Field>
            </div>
            <Field label="Repository folder on the disk"><input className="mono" value={path} onChange={e => setPath(e.target.value)} placeholder={`${usb.mountpoint || '/mnt/usbbackup'}/restic-repo`} /></Field>
            <Check checked={usb.power_off} onChange={v => setUsb({ ...usb, power_off: v })} label="Power the disk down after unmounting" help="Parks the heads so it is safe to unplug." />
          </FormSection>
        )}

        <FormSection title="Password" sub="restic encrypts everything with it. Without it the backups cannot be read — keep a copy somewhere safe (a password manager).">
          <Segmented value={pwMode} onChange={setPwMode} size="sm" label="Password" options={[
            ...(repo ? [{ value: 'keep' as PwMode, label: 'Keep the current one' }] : [{ value: 'create' as PwMode, label: 'New repository, make one up' }]),
            { value: 'password', label: repo ? 'Type a new one' : 'It exists — type its password' },
            { value: 'file', label: 'Read it from a file' },
          ]} />
          {pwMode === 'create' && <span className="muted small">A new, empty repository is created there with a strong password. You can show the password later on the repository.</span>}
          {pwMode === 'password' && <input type="password" value={password} onChange={e => setPassword(e.target.value)} autoComplete="new-password" placeholder="Repository password" />}
          {pwMode === 'file' && <PathInput value={pwFile} onChange={setPwFile} files placeholder="/home/me/backups/restic-password" />}
        </FormSection>

        <FormSection title="Options">
          <Field label="Compression">
            <Segmented size="sm" value={opts.compression || 'auto'} onChange={v => setOpts({ ...opts, compression: v })} label="Compression" options={[{ value: 'auto', label: 'Normal' }, { value: 'max', label: 'Smallest' }, { value: 'off', label: 'Off' }]} />
          </Field>
          <Check checked={opts.background} onChange={v => setOpts({ ...opts, background: v })} label="Work in the background" help="Lowest CPU and disk priority, so the apps stay quick while it runs." />
        </FormSection>

        {managed ? (
          <FormSection title="Checks and clean-up" sub="A check reads the repository to catch damage early. If one fails, copies of it to other places pause until it is repaired.">
            <Field label="Check" group><ScheduleInput value={check} onChange={setCheck} manualLabel="Never" /></Field>
            <Field label="Also read this share of the data (%)" help="0 = structure only; 100 = every byte (slow)."><NumInput value={checkPct} onChange={setCheckPct} max={100} placeholder="0" /></Field>
            <Field label="Prune (free the space of forgotten snapshots)" group help="Jobs can prune after each run instead."><ScheduleInput value={prune} onChange={setPrune} manualLabel="Never" /></Field>
          </FormSection>
        ) : <div className="callout info"><Icon name="info" size={15} /><span>Backrest runs this repository’s checks and prunes. Its hooks tell this app how they went.</span></div>}
      </div>
    </Modal>
  )
}
