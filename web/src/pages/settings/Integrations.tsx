import { useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { useData } from '../../lib/data'
import { ago } from '../../lib/format'
import type { Integrations } from '../../lib/types'
import { Icon } from '../../components/icons'
import { PathInput } from '../../components/pickers'
import { Select } from '../../components/Select'
import { errMsg, Field, Loading, Spinner, useAsync, useToast } from '../../components/ui'
import { Section, StatusDot } from './common'

export function IntegrationsTab() {
  const { repos } = useData()
  const { toast } = useToast()
  const d = useAsync(() => api.get<Integrations>(`${V1}/integrations`), [])
  const [html, setHtml] = useState('')
  const [yaml, setYaml] = useState('')
  const [repo, setRepo] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { if (d.data) { setHtml(d.data.report_html_path); setYaml(d.data.report_widgets_yaml); setRepo(d.data.report_repo_id) } }, [d.data])
  if (d.error) return <div className="callout danger"><Icon name="alert" size={15} /><span>{d.error}</span></div>
  if (!d.data) return <Loading />
  const x = d.data
  const save = async () => {
    setBusy(true)
    try { d.setData(await api.put<Integrations>(`${V1}/integrations`, { report_html_path: html, report_widgets_yaml: yaml, report_repo_id: repo })); toast('Saved') } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const dirty = html !== x.report_html_path || yaml !== x.report_widgets_yaml || repo !== x.report_repo_id

  return (
    <div className="stack loose">
      <Section title="Homepage" sub="Jobs with “Update the Homepage report” rewrite these files after each good run.">
        <div className="stack">
          <Field label="Size report (HTML)" help="A small page with each app’s share of the newest snapshot, for an iframe tile — e.g. Homepage’s config/images/backup-report.html.">
            <PathInput value={html} onChange={setHtml} files placeholder="/home/me/homepage/config/images/backup-report.html" />
          </Field>
          <Field label="Greeting line in widgets.yaml" help="Its “text: Backup … MiB · N snapshots” line is updated with the repository’s size.">
            <PathInput value={yaml} onChange={setYaml} files placeholder="/home/me/homepage/config/widgets.yaml" />
          </Field>
          <Field label="Repository the report is about" help="Empty = the repository of the job that runs.">
            <Select value={repo} onChange={setRepo} options={[{ value: '', label: 'The job’s own repository' }, ...repos.map(r => ({ value: String(r.id), label: r.name }))]} />
          </Field>
          <div className="row"><button className="btn primary" disabled={!dirty || busy} onClick={save}>{busy && <Spinner />}Save</button>{x.report_last && <span className="muted small">last written {ago(x.report_last)}</span>}</div>
          <p className="muted small" style={{ margin: 0 }}>For a Homepage card, use a <code>customapi</code> widget on <code>/api/v1/widget</code> with an API token from <Link to="/settings/tokens">Tokens</Link>: it returns health, last_backup_ago, failing and stored.</p>
        </div>
      </Section>

      <Section title="Backrest">
        <div className="row">
          <StatusDot status={x.backrest.configured ? 'ok' : 'off'} />
          <span>{x.backrest.configured ? 'Connected' : 'Not configured (BACKREST_URL)'}</span>
          {x.backrest.url && <code className="muted small">{x.backrest.url}</code>}
          <span className="spacer" />
          <Link to="/backrest" className="btn sm">Backrest page</Link>
        </div>
      </Section>

      <Section title="Underneath">
        <dl className="set-dl">
          {Object.entries(x.tools || {}).map(([k, v]) => <div key={k}><dt>{k}</dt><dd><code>{v}</code></dd></div>)}
          {!x.tools?.restic && <div><dt>restic</dt><dd className="danger-text">not found</dd></div>}
          <div><dt>Host access</dt><dd>{x.host_access ? 'On — commands on the host and USB disks work' : 'Off (HOST_ACCESS=false): no commands on the host, no USB disk mounting'}</dd></div>
          <div><dt>Staging folder</dt><dd><code>{x.staging_dir}</code></dd></div>
        </dl>
      </Section>
    </div>
  )
}
