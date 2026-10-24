import { useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { fmtBytes, fmtMs, friendlyTime, KIND_LABEL } from '../lib/format'
import type { RunDetail } from '../lib/types'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { RunBadge, StepIcon } from '../components/Status'
import { Card, copyText, Empty, errMsg, Loading, useToast } from '../components/ui'

const TRIGGER: Record<string, string> = { manual: 'started by hand', schedule: 'on its schedule', backrest: 'by Backrest', disk: 'when the disk was plugged in', hook: 'by a hook', api: 'through the API' }

export default function RunPage() {
  const { id } = useParams()
  const { h12 } = useApp()
  const { toast } = useToast()
  const [d, setD] = useState<RunDetail | null>(null)
  const [err, setErr] = useState('')
  const [follow, setFollow] = useState(true)
  const logRef = useRef<HTMLPreElement>(null)
  const running = d?.run.status === 'running'

  useEffect(() => {
    let stop = false
    let t: ReturnType<typeof setTimeout>
    const tick = async () => {
      try {
        const x = await api.get<RunDetail>(`${V1}/runs/${id}`)
        if (stop) return
        setD(x)
        if (x.run.status === 'running') t = setTimeout(tick, 2000)
      } catch (e) { if (!stop) setErr(errMsg(e)) }
    }
    setD(null); setErr('')
    tick()
    return () => { stop = true; clearTimeout(t) }
  }, [id])
  useEffect(() => { if (follow && logRef.current) logRef.current.scrollTop = logRef.current.scrollHeight }, [d?.run.log, follow])

  const cancel = async () => {
    try { await api.post(`${V1}/runs/${id}/cancel`); toast('Stopping…') } catch (e) { toast(errMsg(e), 'err') }
  }
  if (err) return <Shell title="Run"><Empty title="Run not found" icon="history">{err}</Empty></Shell>
  if (!d) return <Shell title="Run"><Loading /></Shell>
  const r = d.run
  const pct = d.percent ? Math.round(d.percent * 100) : 0

  return (
    <Shell title={r.title || KIND_LABEL[r.kind] || 'Run'} sub={`${KIND_LABEL[r.kind] || r.kind} · ${TRIGGER[r.trigger] || r.trigger}`} loading={running}
      controls={running ? <button className="btn danger sm" onClick={cancel}><Icon name="stop" size={13} />Stop</button> : undefined}>
      <div className="stack loose">
        <section className="run-head">
          <RunBadge status={r.status} />
          <span className="muted small">{friendlyTime(r.started_at, h12)}{r.finished_at ? ` → ${friendlyTime(r.finished_at, h12)}` : ''} · {fmtMs(r.duration_ms)}</span>
          {r.job_id && <Link to={`/jobs/${r.job_id}`} className="small"><Icon name="briefcase" size={12} /> {r.job_name || 'the job'}</Link>}
          {r.repository_id && <Link to={`/places/repositories/${r.repository_id}`} className="small"><Icon name="hard-drive" size={12} /> {r.repository_name || 'the repository'}</Link>}
        </section>
        {running && (
          <div className="live big"><span className="live-line"><Icon name="refresh" size={13} className="spin" /><span className="ellipsis">{d.step || 'Working…'}</span>{pct > 0 && pct < 100 && <span className="tnum muted">{pct}%</span>}</span>
            <span className="live-bar"><span style={{ width: `${pct}%` }} className={pct ? '' : 'indeterminate'} /></span></div>
        )}
        {r.error && <div className="callout danger"><Icon name="x-circle" size={15} /><span>{r.error}</span></div>}
        {r.summary && !running && <div className="callout info"><Icon name="info" size={15} /><span>{r.summary}</span></div>}

        {r.snapshot_id && (
          <div className="stat-row">
            <div className="stat"><span className="stat-n mono">{r.snapshot_id.slice(0, 8)}</span><span className="stat-l">snapshot</span></div>
            {r.bytes_total > 0 && <div className="stat"><span className="stat-n tnum">{fmtBytes(r.bytes_total)}</span><span className="stat-l">backed up</span></div>}
            {r.bytes_added > 0 && <div className="stat"><span className="stat-n tnum">{fmtBytes(r.bytes_added)}</span><span className="stat-l">new data stored</span></div>}
            {(r.files_new > 0 || r.files_changed > 0) && <div className="stat"><span className="stat-n tnum">{r.files_new} / {r.files_changed}</span><span className="stat-l">new / changed files</span></div>}
          </div>
        )}

        <Card title="Steps" flush>
          {r.steps.length === 0 ? <div className="card-pad muted small">No steps yet.</div> : (
            <ol className="timeline">
              {r.steps.map((s, i) => (
                <li key={i} className={`tl-step ${s.status}`}>
                  <StepIcon status={s.status} />
                  <div className="grow min0">
                    <div className="row"><span className="tl-name grow">{s.name}</span><span className="muted xsmall tnum">{s.status === 'running' ? '…' : fmtMs(s.ms)}</span></div>
                    {s.detail && <div className="tl-detail small">{s.detail}</div>}
                  </div>
                </li>
              ))}
            </ol>
          )}
        </Card>

        <Card title="Log" actions={<>
          {running && <label className="inline-switch small"><input type="checkbox" checked={follow} onChange={e => setFollow(e.target.checked)} />Follow</label>}
          <button className="btn ghost sm" onClick={async () => toast(await copyText(r.log || '') ? 'Log copied' : 'Could not copy')}><Icon name="copy" size={13} />Copy</button>
        </>} flush>
          <pre ref={logRef} className="log">{r.log || 'Nothing logged.'}</pre>
        </Card>
      </div>
    </Shell>
  )
}
