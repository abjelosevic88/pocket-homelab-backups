import { useEffect, useState } from 'react'
import { api, qs, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { useData } from '../../lib/data'
import type { ConnConfig, ConnKind, ConnKindId, ConnKinds, Connection } from '../../lib/types'
import { CONFIG_KEYS, CONN_ICON, CORE_HINT, CORE_LINK, SIGN_IN } from '../../pages/connKinds'
import { Icon } from '../icons'
import { errMsg, Field, Loading, Modal, Segmented, Spinner, useToast } from '../ui'

export function ConnEditor({ conn, kind: initialKind, onClose }: { conn?: Connection; kind?: ConnKindId; onClose: () => void }) {
  const { reload } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const [kinds, setKinds] = useState<ConnKind[] | null>(null)
  const [kind, setKind] = useState<ConnKindId | null>(conn?.kind || initialKind || null)
  const [name, setName] = useState(conn?.name || '')
  const [config, setConfig] = useState<ConnConfig>(() => ({ ...(conn?.config || {}) }))
  const [secret, setSecret] = useState<Record<string, string>>({})
  const [options, setOptions] = useState(() => Object.entries(conn?.config.options || {}).map(([k, v]) => `${k} = ${v}`).join('\n'))
  const [scope, setScope] = useState(conn?.config.scope || 'drive')
  const [busy, setBusy] = useState(false)
  useEffect(() => { api.get<ConnKinds>(`${V1}/connections/kinds`).then(k => setKinds(k.kinds)).catch(e => toast(errMsg(e), 'err')) }, [toast])
  const k = kinds?.find(x => x.kind === kind)

  const signIn = async () => {
    if (!kind) return
    setBusy(true)
    try {
      const r = await api.get<{ url: string }>(`${V1}/connections/oauth/${kind}/start${qs({ json: 1, name: name.trim() || k?.name, scope: kind === 'gdrive' ? scope : undefined, connection: conn?.id })}`)
      window.location.href = r.url
    } catch (e) { toast(errMsg(e), 'err'); setBusy(false) }
  }

  const save = async () => {
    if (!kind) return
    setBusy(true)
    try {
      const body = { name: name.trim() || k?.name, kind, config: { ...config, scope: kind === 'gdrive' ? scope : config.scope }, secret, options: kind === 'rclone' ? options : '' }
      const c = conn ? await api.put<Connection>(`${V1}/connections/${conn.id}`, body) : await api.post<Connection>(`${V1}/connections`, body)
      toast(c.status === 'error' ? `Saved, but it does not answer: ${c.last_error}` : 'Connected', c.status === 'error' ? 'err' : 'ok')
      await reload(); bump()
      onClose()
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }

  const setField = (key: string, secretField: boolean | undefined, v: string) => {
    if (secretField) setSecret(s => ({ ...s, [key]: v }))
    else if (CONFIG_KEYS.includes(key)) setConfig(c => ({ ...c, [key]: key === 'port' ? (v ? Number(v) : undefined) : v }))
  }
  const fieldValue = (key: string, secretField?: boolean) => secretField ? (secret[key] || '') : String((config as Record<string, unknown>)[key] ?? '')

  if (!kinds) return <Modal title="Add a connection" onClose={onClose}><Loading /></Modal>

  if (!kind) {
    return (
      <Modal title="Add a connection" onClose={onClose} size="lg">
        <p className="muted small">A connection is a place outside this box: a cloud account or another server. Backups can be kept there, or copied there after each run.</p>
        <div className="kind-grid wide">
          {kinds.map(x => (
            <button type="button" key={x.kind} className={`kind-card ${x.auth === 'core' && !x.ready ? 'dim' : ''}`} onClick={() => { setKind(x.kind); if (!name) setName(x.name.replace(/ \(.*\)$/, '')) }}>
              <Icon name={CONN_ICON[x.kind]} size={18} /><span>{x.name}</span>
              {x.auth === 'core' && !x.ready && <span className="kind-tag">Needs Pocket Core</span>}
            </button>
          ))}
        </div>
      </Modal>
    )
  }

  const oauth = k?.auth === 'oauth'
  const core = k?.auth === 'core'
  const coreOff = core && !k?.ready
  const hasToken = !!secret.token?.trim()
  const fields = (k?.fields || []).filter(f => !(oauth && f.key === 'token'))
  return (
    <Modal title={conn ? `Edit ${conn.name}` : `Connect ${k?.name || kind}`} onClose={onClose} size="lg" footer={<>
      {!conn && <button className="btn ghost" onClick={() => setKind(null)}><Icon name="chevron-left" size={14} />Other kinds</button>}
      <span className="spacer" />
      <button className="btn" onClick={onClose}>Cancel</button>
      {(!oauth || hasToken || conn) && <button className="btn primary" disabled={busy || coreOff} onClick={save}>{busy && <Spinner />}{conn ? 'Save' : 'Connect'}</button>}
    </>}>
      <div className="stack">
        <Field label="Name"><input value={name} onChange={e => setName(e.target.value)} placeholder={k?.name} /></Field>

        {core && (
          <div className={`core-box ${coreOff ? 'off' : ''}`}>
            <Icon name="folder" size={20} />
            <div className="grow">
              <div className="core-title">Pocket Files {coreOff && <span className="lvl-badge sm off">Needs Pocket Core</span>}</div>
              <p className="muted small">{coreOff ? CORE_HINT : 'Backups go into your own Pocket Files through Pocket Core: nothing to type, and Core can switch it off at any time.'}</p>
              {coreOff && <a href={CORE_LINK} target="_blank" rel="noreferrer" className="small">Learn more about Pocket Core <Icon name="external" size={12} /></a>}
            </div>
          </div>
        )}

        {oauth && (
          <div className="stack tight">
            {kind === 'gdrive' && (
              <Field label="What Pocket Backups may see in your Drive" group help={scope === 'drive.readonly' ? 'Read-only suits bringing your Drive home (a pull); backups cannot be written.' : scope === 'drive.file' ? 'Only files Pocket Backups itself made — the safest choice for keeping backups there.' : 'Everything — needed to use folders you made yourself.'}>
                <Segmented size="sm" value={scope} onChange={setScope} label="Scope" options={[{ value: 'drive', label: 'All files' }, { value: 'drive.file', label: 'Only its own files' }, { value: 'drive.readonly', label: 'Read only' }]} />
              </Field>
            )}
            <button className="btn primary lg" disabled={busy || !k?.ready} onClick={signIn}><Icon name="key" size={16} />{SIGN_IN[kind] || 'Sign in'}</button>
            {!k?.ready && <div className="callout warn"><Icon name="info" size={15} /><span>{k?.help}</span></div>}
            <details className="disclose" open={!k?.ready}>
              <summary>Paste a token from rclone instead</summary>
              <div className="stack tight mt-s">
                <p className="muted small" style={{ margin: 0 }}>On a computer with a browser run <code>rclone authorize "{kind === 'gdrive' ? 'drive' : kind}"</code>, sign in, and paste the token it prints (the part in curly braces).</p>
                <textarea className="mono" rows={3} value={secret.token || ''} onChange={e => setSecret(s => ({ ...s, token: e.target.value }))} placeholder='{"access_token":"…","refresh_token":"…"}' spellCheck={false} />
              </div>
            </details>
          </div>
        )}

        {!oauth && !core && kind !== 'rclone' && fields.map(f => (
          <Field key={f.key} label={<>{f.label}{f.optional && <span className="faint"> · optional</span>}</>} help={f.secret && conn?.has_secret ? 'Saved — type to replace' : f.help}>
            {f.key === 'private_key'
              ? <textarea className="mono" rows={3} value={fieldValue(f.key, f.secret)} onChange={e => setField(f.key, f.secret, e.target.value)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" spellCheck={false} />
              : <input className={f.secret ? '' : 'mono'} type={f.secret ? 'password' : 'text'} autoComplete={f.secret ? 'new-password' : 'off'} value={fieldValue(f.key, f.secret)} placeholder={f.secret && conn?.has_secret ? '••••••' : f.placeholder} onChange={e => setField(f.key, f.secret, e.target.value)} />}
          </Field>
        ))}

        {kind === 'rclone' && (
          <Field label="rclone options" help="One per line as in rclone.conf (type = …). Secret ones (pass, key, token…) are sealed in the database.">
            <textarea className="mono" rows={7} value={options} onChange={e => setOptions(e.target.value)} placeholder={'type = s3\nprovider = Other\nendpoint = https://…'} spellCheck={false} />
          </Field>
        )}

        {!core && kind !== 'rclone' && (
          <Field label={<>Folder everything goes under<span className="faint"> · optional</span></>} help="Paths of repositories and copies on this connection start here.">
            <input className="mono" value={config.root || ''} onChange={e => setConfig(c => ({ ...c, root: e.target.value }))} placeholder={kind === 'sftp' ? '/mnt/tank/backups' : 'Backups'} />
          </Field>
        )}
        {conn?.config.imported && <span className="muted small">Imported from {conn.config.imported}.</span>}
      </div>
    </Modal>
  )
}
