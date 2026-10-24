import { useEffect, useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { useData } from '../../lib/data'
import type { Integrations, Source, SourceConfig, SourceKind } from '../../lib/types'
import { KIND_HELP, KIND_ICON, KIND_ONE, KIND_ORDER } from '../../pages/sourceKinds'
import type { SourcePrefill } from '../dialogs'
import { Icon } from '../icons'
import { ListInput, PathInput } from '../pickers'
import { errMsg, Field, Modal, Spinner, useToast } from '../ui'
import { Check, FormSection, MultiPick } from './common'

export function SourceEditor({ source, prefill, onClose }: { source?: Source; prefill?: SourcePrefill; onClose: () => void }) {
  const { jobs, reload } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const [kind, setKind] = useState<SourceKind>(source?.kind || prefill?.kind || 'paths')
  const [name, setName] = useState(source?.name || prefill?.name || '')
  const [c, setC] = useState<SourceConfig>(() => ({ ...(source?.config || prefill?.config || {}) }))
  const [project, setProject] = useState(source?.project || prefill?.project || '')
  const [notes, setNotes] = useState(source?.notes || '')
  const [password, setPassword] = useState('')
  const [addTo, setAddTo] = useState<number[]>(prefill?.jobs || (jobs.length === 1 ? [jobs[0].id] : []))
  const [busy, setBusy] = useState(false)
  const [hostAccess, setHostAccess] = useState(true)
  useEffect(() => { api.get<Integrations>(`${V1}/integrations`).then(i => setHostAccess(i.host_access)).catch(() => {}) }, [])
  const set = <K extends keyof SourceConfig>(k: K, v: SourceConfig[K]) => setC(x => ({ ...x, [k]: v }))
  const container = kind === 'postgres' || kind === 'mariadb' || kind === 'container_files' || kind === 'container_command'

  const save = async () => {
    setBusy(true)
    try {
      const body: Record<string, unknown> = { name: name.trim(), kind, config: c, project: project.trim(), notes }
      if (password) body.password = password
      if (!source) body.jobs = addTo
      if (source) await api.put(`${V1}/sources/${source.id}`, body)
      else await api.post(`${V1}/sources`, body)
      toast(source ? 'Source saved' : 'Source added')
      await reload(); bump()
      onClose()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }

  return (
    <Modal title={source ? `Edit ${source.name}` : 'Protect something new'} onClose={onClose} size="xl" footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={busy || !name.trim()} onClick={save}>{busy && <Spinner />}{source ? 'Save' : 'Add'}</button>
    </>}>
      <div className="stack">
        {!source && (
          <div className="kind-grid">
            {KIND_ORDER.map(k => (
              <button type="button" key={k} className={`kind-card ${k === kind ? 'active' : ''} ${k === 'host_command' && !hostAccess ? 'dim' : ''}`} onClick={() => setKind(k)}>
                <Icon name={KIND_ICON[k]} size={18} /><span>{KIND_ONE[k]}</span>
              </button>
            ))}
          </div>
        )}
        <p className="muted small" style={{ margin: 0 }}>{KIND_HELP[kind]}</p>
        {kind === 'host_command' && !hostAccess && <div className="callout warn"><Icon name="alert" size={15} /><span>Host access is off. Commands on the host need HOST_ACCESS=true for Pocket Backups (it runs a short-lived privileged helper container).</span></div>}

        <div className="two-col">
          <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} placeholder="Nextcloud database" autoFocus={!!source} /></Field>
          <Field label="App (compose project)" help="Lets the coverage check know this app is covered."><input value={project} onChange={e => setProject(e.target.value)} placeholder="nextcloud" /></Field>
        </div>

        {kind === 'paths' && (
          <FormSection title="Folders">
            <Field label="Folders to back up" help="One absolute path per line."><ListInput value={c.paths} onChange={v => set('paths', v)} rows={4} placeholder="/home/me/documents" /></Field>
            <Field label="Leave out" help="Patterns, one per line: **/cache, *.log, /home/me/documents/tmp"><ListInput value={c.excludes} onChange={v => set('excludes', v)} rows={3} /></Field>
          </FormSection>
        )}

        {kind === 'sqlite' && (
          <FormSection title="Database file">
            <Field label="The .db / .sqlite file"><PathInput value={c.path || ''} onChange={v => set('path', v)} files placeholder="/home/me/app/data/app.db" /></Field>
            <Check checked={c.skip_if_missing} onChange={v => set('skip_if_missing', v)} label="Skip quietly while the file does not exist yet" help="For an app that creates its database on first use." />
          </FormSection>
        )}

        {container && (
          <FormSection title="Container">
            <Field label="Container name"><input className="mono" value={c.container || ''} onChange={e => set('container', e.target.value)} placeholder="nextcloud_db" /></Field>
            <Check checked={c.skip_if_missing} onChange={v => set('skip_if_missing', v)} label="Skip quietly while the container is stopped" />
          </FormSection>
        )}

        {(kind === 'postgres' || kind === 'mariadb') && (
          <FormSection title="Database" sub="Leave these empty to use what the container itself was set up with (its POSTGRES_* or MARIADB_* settings).">
            <div className="three-col">
              <Field label="Database"><input className="mono" value={c.database || ''} onChange={e => set('database', e.target.value)} placeholder="(from the container)" /></Field>
              <Field label="User"><input className="mono" value={c.user || ''} onChange={e => set('user', e.target.value)} placeholder={kind === 'mariadb' ? 'root' : '(from the container)'} /></Field>
              <Field label="Password" help={source?.has_secret ? 'Saved — type to replace' : 'Usually not needed'}><input type="password" value={password} onChange={e => setPassword(e.target.value)} autoComplete="new-password" placeholder={source?.has_secret ? '••••••' : ''} /></Field>
            </div>
            {kind === 'mariadb' && <Field label="…or the container variable holding the password"><input className="mono" value={c.password_env || ''} onChange={e => set('password_env', e.target.value)} placeholder="MARIADB_ROOT_PASSWORD" /></Field>}
            <Field label="Tables whose rows are left out" help="The table itself stays; its rows are skipped (search indexes, caches). One per line."><ListInput value={c.exclude_table_data} onChange={v => set('exclude_table_data', v)} rows={2} /></Field>
            <Field label="Extra arguments" help="One per line, passed to the dump tool."><ListInput value={c.extra_args} onChange={v => set('extra_args', v)} rows={2} /></Field>
          </FormSection>
        )}

        {kind === 'volume' && (
          <FormSection title="Volume">
            <Field label="Volume name" help="docker volume ls shows them."><input className="mono" value={c.volume || ''} onChange={e => set('volume', e.target.value)} placeholder="myapp_data" /></Field>
          </FormSection>
        )}

        {kind === 'container_files' && (
          <FormSection title="Files in the container">
            <Field label="Folder in the container"><input className="mono" value={c.path || ''} onChange={e => set('path', e.target.value)} placeholder="/data" /></Field>
            <Field label="Only these entries" help="Inside that folder, one per line. Empty = everything."><ListInput value={c.files} onChange={v => set('files', v)} rows={2} placeholder="ssh" /></Field>
            <Field label="Run as user in the container" help="Empty = the container’s own user."><input className="mono" value={c.user || ''} onChange={e => set('user', e.target.value)} placeholder="root" /></Field>
          </FormSection>
        )}

        {(kind === 'container_command' || kind === 'host_command') && (
          <FormSection title="Command" sub="Its output is saved to the file below.">
            <Field label="Shell command"><textarea className="mono" rows={4} value={c.command || ''} onChange={e => set('command', e.target.value)} placeholder={kind === 'host_command' ? 'dpkg --get-selections' : 'php occ app:list --output=json'} spellCheck={false} /></Field>
            {kind === 'host_command'
              ? <Field label="Run as" help="A user name on the host. Empty = root."><input className="mono" value={c.run_as || ''} onChange={e => set('run_as', e.target.value)} placeholder="root" /></Field>
              : <Field label="Run as user in the container" help="Empty = the container’s own user."><input className="mono" value={c.user || ''} onChange={e => set('user', e.target.value)} /></Field>}
          </FormSection>
        )}

        {kind !== 'paths' && (
          <FormSection title="The file it makes" sub="Written into the job’s staging folder, which the snapshot includes.">
            <Field label="File name" help="Relative to the staging folder. Empty = a name made from the source’s name."><input className="mono" value={c.output || ''} onChange={e => set('output', e.target.value)} placeholder={source?.output || 'db/myapp.sql'} /></Field>
            <div className="row wrap" style={{ gap: 18 }}>
              {kind !== 'volume' && kind !== 'sqlite' && <Check checked={c.compress} onChange={v => set('compress', v)} label="Compress (gzip)" />}
              <Check checked={c.private} onChange={v => set('private', v)} label="Only readable by the owner" help="For keys and password hashes (file mode 0600)." />
            </div>
            {(kind === 'container_files' || kind === 'container_command' || kind === 'host_command') && (
              <Field label="Exit codes that still count as success" help="e.g. 1 for tar’s “a file changed while it was read”. Comma-separated.">
                <input className="mono" value={(c.ok_exit_codes || []).join(', ')} onChange={e => set('ok_exit_codes', e.target.value.split(/[\s,]+/).filter(Boolean).map(Number).filter(n => !isNaN(n)))} placeholder="1" style={{ width: 160 }} />
              </Field>
            )}
          </FormSection>
        )}

        {!source && jobs.length > 0 && (
          <FormSection title="Add it to these jobs">
            <MultiPick value={addTo} onChange={setAddTo} options={jobs.filter(j => j.kind === 'backup').map(j => ({ value: j.id, label: j.name, sub: j.engine === 'backrest' ? 'Backrest' : '' }))} />
          </FormSection>
        )}

        <Field label="Notes" help="Why it is backed up this way — for whoever looks next."><textarea rows={2} value={notes} onChange={e => setNotes(e.target.value)} /></Field>
      </div>
    </Modal>
  )
}

