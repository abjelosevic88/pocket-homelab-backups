import { useSearchParams } from 'react-router-dom'
import { useData } from '../lib/data'
import { fmtBytes } from '../lib/format'
import { Icon } from '../components/icons'
import { Select } from '../components/Select'
import { Shell } from '../components/Shell'
import { SnapshotBrowser } from '../components/SnapshotBrowser'
import { Card, Empty, Loading } from '../components/ui'

export default function Restore() {
  const { repos, loaded } = useData()
  const [params, setParams] = useSearchParams()
  const id = Number(params.get('repo')) || repos[0]?.id || 0
  const repo = repos.find(r => r.id === id)
  return (
    <Shell title="Restore" controls={repos.length > 1 ? (
      <Select value={id} onChange={v => setParams({ repo: String(v) }, { replace: true })} label="Repository" size="sm" width={280}
        options={repos.map(r => ({ value: r.id, label: r.name, hint: r.stats.updated_at ? fmtBytes(r.stats.size) : undefined, icon: <Icon name={r.location === 'usb' ? 'usb' : r.location === 'connection' ? 'cloud' : 'hard-drive'} size={14} /> }))} />
    ) : undefined}>
      {!loaded ? <Loading /> : !repo ? <Empty title="Nothing to restore from yet" icon="rotate">Snapshots appear here once a job has run.</Empty> : (
        <div className="stack">
          <p className="muted" style={{ margin: 0 }}>Get a file or a folder back from any snapshot: download it, or put it back on the box.</p>
          <Card title={<span className="row"><Icon name="archive" size={16} />{repo.name}</span>}>
            <SnapshotBrowser key={repo.id} repo={repo} />
          </Card>
        </div>
      )}
    </Shell>
  )
}
