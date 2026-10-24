import { useData } from '../lib/data'
import { JobCard } from '../components/cards'
import { openJobEditor } from '../components/dialogs'
import { Icon } from '../components/icons'
import { Shell } from '../components/Shell'
import { Empty, Loading } from '../components/ui'

export default function Jobs() {
  const { jobs, loaded } = useData()
  const backups = jobs.filter(j => j.kind === 'backup')
  const pulls = jobs.filter(j => j.kind === 'pull')
  return (
    <Shell title="Jobs" controls={<button className="btn primary sm" onClick={() => openJobEditor()}><Icon name="plus" size={14} />New job</button>}>
      {!loaded ? <Loading /> : jobs.length === 0 ? (
        <Empty title="No jobs yet" icon="briefcase">A job backs up what you choose to a place you choose, on a schedule. <button className="link-btn" onClick={() => openJobEditor()}>Make the first one</button>.</Empty>
      ) : (
        <div className="stack loose">
          {backups.length > 0 && <section><div className="sec-head"><h2>Backups</h2></div><div className="card-grid">{backups.map(j => <JobCard key={j.id} job={j} />)}</div></section>}
          {pulls.length > 0 && <section><div className="sec-head"><h2>Clouds brought home</h2><span className="muted small">Copies of cloud folders kept on this box</span></div><div className="card-grid">{pulls.map(j => <JobCard key={j.id} job={j} />)}</div></section>}
        </div>
      )}
    </Shell>
  )
}
