import { useState } from 'react'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import type { Coverage as CoverageData, CoverageProject } from '../lib/types'
import { openSourceEditor } from '../components/dialogs'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { Badge } from '../components/Status'
import { errMsg, Field, Loading, Modal, Segmented, Spinner, useAsync, useToast } from '../components/ui'

type Filter = 'all' | 'uncovered' | 'covered' | 'skipped'

export default function Coverage() {
  const { version } = useApp()
  const { toast } = useToast()
  const cov = useAsync(() => api.get<CoverageData>(`${V1}/coverage`), [version])
  const [filter, setFilter] = useState<Filter>('all')
  const [skip, setSkip] = useState<CoverageProject | null>(null)
  const d = cov.data
  const shown = (d?.projects || []).filter(p => filter === 'all' || p.status === filter)

  const unskip = async (p: CoverageProject) => {
    try { cov.setData(await api.post<CoverageData>(`${V1}/coverage/skip`, { project: p.project, skip: false })); toast(`${p.project} is checked again`) } catch (e) { toast(errMsg(e), 'err') }
  }

  return (
    <Shell title="Coverage" loading={cov.loading && !!d} controls={<button className="btn sm" onClick={() => cov.reload()}><Icon name="refresh" size={13} />Check again</button>}>
      {!d ? <Loading label="Looking at the running apps…" /> : (
        <div className="stack loose">
          <p className="muted" style={{ margin: 0 }}>Every app running on this box, and whether a backup covers it. A new app you install shows up here as not covered until you add it to a job — or say it needs no backup.</p>
          {d.error && <div className="callout danger"><Icon name="alert" size={15} /><span>{d.error}</span></div>}
          <div className="stat-row">
            <button type="button" className={`stat clickable ${filter === 'uncovered' ? 'on' : ''}`} onClick={() => setFilter(filter === 'uncovered' ? 'all' : 'uncovered')}><span className={`stat-n tnum ${d.uncovered ? 'danger-text' : ''}`}>{d.uncovered}</span><span className="stat-l">not covered</span></button>
            <button type="button" className={`stat clickable ${filter === 'covered' ? 'on' : ''}`} onClick={() => setFilter(filter === 'covered' ? 'all' : 'covered')}><span className="stat-n tnum">{d.covered}</span><span className="stat-l">covered</span></button>
            <button type="button" className={`stat clickable ${filter === 'skipped' ? 'on' : ''}`} onClick={() => setFilter(filter === 'skipped' ? 'all' : 'skipped')}><span className="stat-n tnum">{d.skipped}</span><span className="stat-l">left out on purpose</span></button>
          </div>
          <Segmented size="sm" value={filter} onChange={setFilter} label="Show" options={[{ value: 'all', label: 'All' }, { value: 'uncovered', label: 'Not covered' }, { value: 'covered', label: 'Covered' }, { value: 'skipped', label: 'Left out' }]} />
          <div className="cov-list">
            {shown.map(p => (
              <div key={p.project} className={`cov-row ${p.status}`}>
                <div className="row wrap">
                  {p.status === 'covered' ? <Badge lvl="ok" icon="shield-check">Covered</Badge> : p.status === 'skipped' ? <Badge lvl="off" icon="minus">Left out</Badge> : <Badge lvl="crit" icon="shield-alert">Not covered</Badge>}
                  <b className="cov-name">{p.project}</b>
                  {p.working_dir && <code className="xsmall muted">{p.working_dir}</code>}
                  <span className="spacer" />
                  {p.status === 'uncovered' && <>
                    <button className="btn sm" onClick={() => openSourceEditor(undefined, { name: p.project, project: p.project, kind: 'paths', config: p.working_dir ? { paths: [p.working_dir] } : {} })}><Icon name="plus" size={13} />Add a source</button>
                    <button className="btn ghost sm" onClick={() => setSkip(p)}>Needs no backup</button>
                  </>}
                  {p.status === 'skipped' && <button className="btn ghost sm" onClick={() => unskip(p)}>Check it again</button>}
                </div>
                <div className="muted xsmall cov-detail">
                  {p.containers.map(c => c.name).join(', ')}
                  {p.by.length > 0 && <> · covered by {p.by.join(', ')}</>}
                  {p.status === 'skipped' && p.reason && <> · {p.reason}</>}
                </div>
                {p.volumes.length > 0 && <div className="row wrap" style={{ gap: 4 }}>{p.volumes.map(v => <span key={v.name} className={`tag ${v.covered ? 'ok' : ''}`} title={v.covered ? 'Exported by a source' : 'Not exported by any source (may be covered by its folder, or not need it)'}><Icon name="box" size={10} />{v.name}</span>)}</div>}
              </div>
            ))}
            {shown.length === 0 && <div className="muted small">Nothing here.</div>}
          </div>
        </div>
      )}
      {skip && <SkipDialog p={skip} onClose={() => setSkip(null)} onDone={x => { cov.setData(x); setSkip(null) }} />}
    </Shell>
  )
}

function SkipDialog({ p, onClose, onDone }: { p: CoverageProject; onClose: () => void; onDone: (d: CoverageData) => void }) {
  const { toast } = useToast()
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const go = async () => {
    setBusy(true)
    try { onDone(await api.post<CoverageData>(`${V1}/coverage/skip`, { project: p.project, reason, skip: true })); toast(`${p.project} is left out`) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  return (
    <Modal title={`Leave ${p.project} out of backups?`} onClose={onClose} size="sm" footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={busy} onClick={go}>{busy && <Spinner />}Leave it out</button>
    </>}>
      <Field label="Why" help="So whoever looks next knows it was on purpose."><input value={reason} onChange={e => setReason(e.target.value)} placeholder="Stateless — nothing to keep" autoFocus /></Field>
    </Modal>
  )
}
