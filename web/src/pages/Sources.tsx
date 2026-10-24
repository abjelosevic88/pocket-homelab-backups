import { useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useData } from '../lib/data'
import { ago, fmtBytes, fmtMs } from '../lib/format'
import type { Run, Source, Suggestion } from '../lib/types'
import { openSourceEditor } from '../components/dialogs'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { Badge } from '../components/Status'
import { Card, Confirm, Empty, errMsg, Loading, MenuButton, Spinner, Switch, useToast } from '../components/ui'
import { KIND_HELP, KIND_ICON, KIND_NAMES, KIND_ORDER } from './sourceKinds'

function describe(s: Source): string {
  const c = s.config
  switch (s.kind) {
    case 'paths': return (c.paths || []).join(', ')
    case 'sqlite': return c.path || ''
    case 'postgres': case 'mariadb': return `${c.container}${c.database ? ' · ' + c.database : ''}`
    case 'volume': return c.volume || ''
    case 'container_files': return `${c.container}:${c.path || '/'}${c.files?.length ? ' (' + c.files.join(', ') + ')' : ''}`
    case 'container_command': return `${c.container}: ${c.command}`
    case 'host_command': return (c.command || '').split('\n')[0]
  }
}

export default function Sources() {
  const { sources, jobs, loaded, reload } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [del, setDel] = useState<Source | null>(null)
  const [disc, setDisc] = useState<Suggestion[] | null>(null)
  const [discBusy, setDiscBusy] = useState(false)
  const [hideAdded, setHideAdded] = useState(true)

  const toggle = async (s: Source, v: boolean) => {
    try { await api.put(`${V1}/sources/${s.id}`, { enabled: v }); await reload(); bump() } catch (e) { toast(errMsg(e), 'err') }
  }
  const tryNow = async (s: Source) => {
    try { const r = await api.post<Run>(`${V1}/sources/${s.id}/run`); navigate(`/runs/${r.id}`) } catch (e) { toast(errMsg(e), 'err') }
  }
  const remove = async () => {
    try { await api.del(`${V1}/sources/${del!.id}`); toast('Removed — files it already wrote stay where they are'); setDel(null); await reload(); bump() } catch (e) { toast(errMsg(e), 'err') }
  }
  const discover = async () => {
    setDiscBusy(true)
    try { setDisc(await api.get<Suggestion[]>(`${V1}/discover`)) } catch (e) { toast(errMsg(e), 'err') } finally { setDiscBusy(false) }
  }
  const jobName = (id: number) => jobs.find(j => j.id === id)?.name || `job ${id}`
  const shown = (disc || []).filter(x => !hideAdded || !x.exists)

  return (
    <Shell title="What’s protected" controls={<>
      <button className="btn sm" disabled={discBusy} onClick={discover}>{discBusy ? <Spinner /> : <Icon name="search" size={13} />}Find things to protect</button>
      <button className="btn primary sm" onClick={() => openSourceEditor()}><Icon name="plus" size={13} />Add</button>
    </>}>
      <div className="stack loose">
        <p className="muted" style={{ margin: 0 }}>Everything the jobs back up. Databases are copied consistently right before each snapshot, so a restore never finds a half-written file.</p>

        {disc && (
          <Card title="Found on this box" sub="Databases in containers, SQLite files in app folders and Docker volumes. Add the ones that matter." actions={<>
            <label className="inline-switch small"><Switch size="sm" checked={hideAdded} onChange={setHideAdded} label="Hide added" />Hide added</label>
            <button className="btn ghost icon-only sm" aria-label="Close" onClick={() => setDisc(null)}><Icon name="x" size={15} /></button>
          </>} flush>
            {shown.length === 0 ? <div className="card-pad muted small">Nothing new found.</div> : (
              <div className="mini-list">
                {shown.map((x, i) => (
                  <div key={i} className="mini-row">
                    <Icon name={KIND_ICON[x.kind]} size={14} className="muted" />
                    <span className="grow min0"><span className="block ellipsis">{x.name}</span><span className="muted xsmall ellipsis block">{x.why}</span></span>
                    {x.exists ? <Badge lvl="ok" icon="check">Already added</Badge> :
                      <button className="btn sm" onClick={() => openSourceEditor(undefined, { name: x.name, kind: x.kind, project: x.project, config: x.config })}><Icon name="plus" size={13} />Add</button>}
                  </div>
                ))}
              </div>
            )}
          </Card>
        )}

        {!loaded ? <Loading /> : sources.length === 0 ? (
          <Empty title="Nothing is protected yet" icon="shield">Add folders, databases or volumes — or let <button className="link-btn" onClick={discover}>Find things to protect</button> look for them.</Empty>
        ) : KIND_ORDER.filter(k => sources.some(s => s.kind === k)).map(k => (
          <Card key={k} title={<span className="row"><Icon name={KIND_ICON[k]} size={16} />{KIND_NAMES[k]}</span>} sub={KIND_HELP[k]} flush>
            <div className="src-list">
              {sources.filter(s => s.kind === k).map(s => (
                <div key={s.id} className={`src-row ${s.enabled ? '' : 'dim'}`}>
                  <Switch size="sm" checked={s.enabled} onChange={v => toggle(s, v)} label={`${s.name} on`} />
                  <button type="button" className="src-main" onClick={() => openSourceEditor(s)}>
                    <span className="src-name">{s.name}{s.project && <span className="muted xsmall"> · {s.project}</span>}</span>
                    <span className="mono xsmall muted ellipsis block">{describe(s)}</span>
                    {s.output && <span className="xsmall faint ellipsis block">→ {s.output}</span>}
                  </button>
                  <span className="src-state">
                    {s.last_error ? <Badge lvl="crit" icon="x-circle" title={s.last_error}>Failed</Badge>
                      : s.last_ok_at ? <span className="muted xsmall">{ago(s.last_ok_at)}{s.last_size ? ` · ${fmtBytes(s.last_size)}` : ''}{s.last_duration_ms ? ` · ${fmtMs(s.last_duration_ms)}` : ''}</span>
                        : s.kind === 'paths' ? null : <span className="faint xsmall">not yet</span>}
                    <span className="xsmall muted ellipsis">{s.jobs.length ? s.jobs.map(jobName).join(', ') : <span className="danger-text">in no job</span>}</span>
                  </span>
                  <MenuButton label={`Options for ${s.name}`} className="btn ghost icon-only xs" align="right" items={[
                    ...(s.kind !== 'paths' ? [{ label: 'Try now', icon: 'play', onSelect: () => tryNow(s) }] : []),
                    { label: 'Edit', icon: 'edit', onSelect: () => openSourceEditor(s) },
                    'sep' as const,
                    { label: 'Remove', icon: 'trash', danger: true, onSelect: () => setDel(s) },
                  ]}><Icon name="more" size={15} /></MenuButton>
                </div>
              ))}
            </div>
            {sources.filter(s => s.kind === k && s.last_error).map(s => <div key={s.id} className="callout danger m small"><Icon name="alert" size={14} /><span><b>{s.name}:</b> {s.last_error}</span></div>)}
          </Card>
        ))}
      </div>
      {del && <Confirm title={`Stop protecting ${del.name}?`} message="It is taken out of every job. Its last capture stays in the staging folder and in the snapshots already made." onConfirm={remove} onCancel={() => setDel(null)} confirmLabel="Remove" />}
    </Shell>
  )
}
