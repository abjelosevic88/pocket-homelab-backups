import { useEffect, useState } from 'react'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import type { Settings } from '../../lib/types'
import { Icon } from '../../components/icons'
import { errMsg, Field, Loading, Segmented, SettingRow, Spinner, Switch, useToast } from '../../components/ui'
import { Section, useSaveSettings } from './common'

const KINDS: { value: Settings['notify_kind']; label: string; help: string; placeholder: string; token: string }[] = [
  { value: 'apprise', label: 'Apprise', help: 'An Apprise API route — it fans out to ntfy, Pushover, Telegram, email and 100+ others.', placeholder: 'https://apprise.example.com/notify/backups', token: 'Usually not needed' },
  { value: 'ntfy', label: 'ntfy', help: 'An ntfy topic URL. Install the ntfy app and subscribe to the same topic.', placeholder: 'https://ntfy.sh/my-backups', token: 'Access token for a protected topic (optional)' },
  { value: 'webhook', label: 'Webhook', help: 'A JSON POST with title, body, level and link to any URL you choose.', placeholder: 'https://example.com/hooks/backups', token: 'Sent as a Bearer token (optional)' },
]

export function NotificationsTab() {
  const { sr, settings } = useApp()
  const save = useSaveSettings()
  const { toast } = useToast()
  const [url, setUrl] = useState(settings?.notify_url || '')
  const [token, setToken] = useState('')
  const [testing, setTesting] = useState(false)
  useEffect(() => { setUrl(settings?.notify_url || '') }, [settings?.notify_url])
  if (!sr || !settings) return <Loading />
  const s = settings
  const kind = KINDS.find(k => k.value === s.notify_kind) || KINDS[0]
  const dirty = url !== s.notify_url || token !== ''

  const saveRoute = async () => {
    const patch: Partial<Settings> & { notify_token?: string } = { notify_url: url.trim() }
    if (token) patch.notify_token = token
    if (await save(patch, 'Notification address saved')) setToken('')
  }
  const test = async () => {
    setTesting(true)
    try { await api.post(`${V1}/notify/test`); toast('Test sent — check your phone') } catch (e) { toast(errMsg(e), 'err') } finally { setTesting(false) }
  }

  return (
    <div className="stack loose">
      <Section title="Where to send" sub="Messages are sent by the server, so they arrive even when no one has the app open.">
        {sr.notify_box_route && <div className="callout info mb"><Icon name="info" size={15} /><span>This box already has a notification route from its setup. It is used for you until you set your own address here.</span></div>}
        <div className="stack">
          <Segmented label="Service" value={s.notify_kind} onChange={v => save({ notify_kind: v })} options={KINDS.map(k => ({ value: k.value, label: k.label }))} />
          <p className="muted small" style={{ margin: 0 }}>{kind.help}</p>
          <Field label="Address"><input value={url} onChange={e => setUrl(e.target.value)} placeholder={kind.placeholder} inputMode="url" autoComplete="off" spellCheck={false} /></Field>
          <Field label="Token" help={kind.token}><input type="password" value={token} onChange={e => setToken(e.target.value)} placeholder={sr.notify_has_token ? '•••• saved — type to replace' : 'Optional'} autoComplete="new-password" /></Field>
          <div className="row">
            <button className="btn primary" disabled={!dirty} onClick={saveRoute}>Save</button>
            <button className="btn" disabled={testing || (!s.notify_url && !sr.notify_box_route)} onClick={test}>{testing ? <Spinner /> : <Icon name="bell" size={14} />}Send a test</button>
            {sr.notify_has_token && <button className="btn ghost sm" onClick={() => save({ notify_token: '' }, 'Token removed')}>Remove token</button>}
          </div>
        </div>
      </Section>

      <Section title="What to send" sub="Each job also says whether it reports good runs.">
        <SettingRow title="When something fails or warns" help="A failed backup, a failed copy, a repository that does not pass its check, an app no backup covers, a USB disk still mounted.">
          <Switch label="Failures and warnings" checked={s.notify_failures} onChange={v => save({ notify_failures: v })} />
        </SettingRow>
        <SettingRow title="When it went well" help="“Backup OK”, “safe to unplug”, “USB disk connected”.">
          <Switch label="Good news" checked={s.notify_successes} onChange={v => save({ notify_successes: v })} />
        </SettingRow>
      </Section>
    </div>
  )
}
