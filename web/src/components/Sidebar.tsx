// Left column: new job, the pages, the jobs with their state.
import { Link, NavLink } from 'react-router-dom'
import { useApp } from '../lib/app-context'
import { useJobs } from '../lib/data'
import { Icon } from './icons'
import { RunDot } from './Status'
import { openJobEditor } from './dialogs'

const NAV: [string, string, string][] = [
  ['/', 'grid', 'Overview'],
  ['/jobs', 'briefcase', 'Jobs'],
  ['/sources', 'shield', 'What’s protected'],
  ['/places', 'hard-drive', 'Places'],
  ['/restore', 'rotate', 'Restore'],
  ['/schedule', 'clock', 'Schedule'],
  ['/runs', 'history', 'Runs'],
  ['/coverage', 'layers', 'Coverage'],
  ['/backrest', 'archive', 'Backrest'],
]

export function Sidebar({ onNavigate }: { onNavigate?: () => void }) {
  const { status, isAdmin } = useApp()
  const jobs = useJobs()
  return (
    <aside className="sidebar" aria-label="Navigation">
      <div className="sb-brand">
        <img src="/favicon.svg" alt="" className="sb-logo" />
        <span className="sb-title">{status?.title && status.title !== 'Pocket Backups' ? status.title : <>Pocket <b>Backups</b></>}</span>
        {status?.version && <span className="sb-version" title={`Version ${status.version}`}>{/^v?\d+\.\d+/.test(status.version) ? 'v' + status.version.replace(/^v/, '').replace(/[-+].*$/, '') : status.version}</span>}
      </div>
      {isAdmin && <button type="button" className="btn primary create-btn" onClick={() => { onNavigate?.(); openJobEditor() }}><Icon name="plus" size={17} strokeWidth={2.2} />New job<kbd className="kbd-on-primary">N</kbd></button>}

      <nav className="sb-nav" aria-label="Pages">
        {(isAdmin ? NAV : NAV.slice(0, 1)).map(([to, icon, label]) => (
          <NavLink key={to} to={to} end={to === '/'} className="sb-nav-item" onClick={onNavigate}><Icon name={icon} size={16} />{label}</NavLink>
        ))}
      </nav>

      {isAdmin && jobs.length > 0 && (
        <section className="sb-sec">
          <h4 className="sb-h"><span className="sb-h-btn">Jobs</span></h4>
          <ul className="srv-list">
            {jobs.map(j => (
              <li key={j.id} className={`srv-row ${j.enabled ? '' : 'paused'}`}>
                <NavLink to={`/jobs/${j.id}`} className="srv-link" onClick={onNavigate}>
                  <RunDot status={j.running ? 'running' : j.enabled ? j.last_run?.status : 'off'} />
                  <span className="srv-name ellipsis">{j.name}</span>
                </NavLink>
              </li>
            ))}
          </ul>
        </section>
      )}

      <div className="sb-foot">
        <Link to="/settings" className="sb-foot-link" onClick={onNavigate}><Icon name="settings" size={15} />Settings</Link>
        <button type="button" className="sb-foot-link" onClick={() => window.dispatchEvent(new CustomEvent('pbk:shortcuts'))}><Icon name="keyboard" size={15} />Shortcuts</button>
      </div>
    </aside>
  )
}
