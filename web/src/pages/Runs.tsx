import { useEffect, useState } from 'react'
import { useSearchParams } from 'react-router-dom'
import { api, qs, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useData } from '../lib/data'
import { KIND_LABEL, RUN_LABEL } from '../lib/format'
import type { Run } from '../lib/types'
import { RunRow } from '../components/cards'
import { Select } from '../components/Select'
import { Shell } from '../components/Shell'
import { Card, Empty, errMsg, Loading, Spinner, useToast } from '../components/ui'

const PAGE = 50

export default function Runs() {
  const { version } = useApp()
  const { jobs } = useData()
  const { toast } = useToast()
  const [params, setParams] = useSearchParams()
  const job = params.get('job') || ''
  const kind = params.get('kind') || ''
  const status = params.get('status') || ''
  const [runs, setRuns] = useState<Run[] | null>(null)
  const [more, setMore] = useState(false)
  const [busy, setBusy] = useState(false)
  const load = (before?: number) => api.get<Run[]>(`${V1}/runs${qs({ job, kind, status, before, limit: PAGE })}`)
  useEffect(() => {
    load().then(r => { setRuns(r); setMore(r.length === PAGE) }).catch(e => toast(errMsg(e), 'err'))
  }, [job, kind, status, version]) // eslint-disable-line react-hooks/exhaustive-deps
  const next = async () => {
    if (!runs?.length) return
    setBusy(true)
    try { const r = await load(runs[runs.length - 1].id); setRuns([...runs, ...r]); setMore(r.length === PAGE) } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }
  const set = (k: string, v: string) => { const p = new URLSearchParams(params); if (v) p.set(k, v); else p.delete(k); setParams(p, { replace: true }) }

  return (
    <Shell title="Runs" controls={<>
      <Select size="sm" value={job} onChange={v => set('job', v)} label="Job" width={240} options={[{ value: '', label: 'Every job' }, ...jobs.map(j => ({ value: String(j.id), label: j.name }))]} />
      <Select size="sm" value={kind} onChange={v => set('kind', v)} label="Kind" options={[{ value: '', label: 'Every kind' }, ...['backup', 'prep', 'post', 'pull', 'check', 'prune', 'restore', 'source', 'hook'].map(k => ({ value: k, label: KIND_LABEL[k] }))]} />
      <Select size="sm" value={status} onChange={v => set('status', v)} label="Status" options={[{ value: '', label: 'Any outcome' }, ...['running', 'success', 'warning', 'failed', 'cancelled'].map(s => ({ value: s, label: RUN_LABEL[s] }))]} />
    </>}>
      <Card flush>
        {!runs ? <Loading /> : runs.length === 0 ? <Empty title="No runs match" icon="history" /> : <div className="run-list">{runs.map(r => <RunRow key={r.id} run={r} />)}</div>}
        {more && <div className="card-pad center"><button className="btn sm" disabled={busy} onClick={next}>{busy && <Spinner />}Older runs</button></div>}
      </Card>
    </Shell>
  )
}
