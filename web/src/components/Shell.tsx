// The app frame every page shares: sidebar (a drawer on narrow screens), top
// bar, command palette, shortcuts and the editors.
import React, { useEffect, useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../lib/api'
import { useApp } from '../lib/app-context'
import { useJobs } from '../lib/data'
import type { Run } from '../lib/types'
import { CommandPalette, type PaletteCommand } from './CommandPalette'
import { openConnEditor, openJobEditor, openRepoEditor, openSourceEditor } from './dialogs'
import { ShortcutsSheet } from './ShortcutsSheet'
import { Sidebar } from './Sidebar'
import { TopBar } from './TopBar'
import { errMsg, modalOpen, useIsMobile, useMediaQuery, useToast } from './ui'

const SIDEBAR_KEY = 'pbk-sidebar'

const PAGES: [string, string, string, string, string][] = [
  ['/', 'Overview', 'grid', 'o', 'home dashboard status'],
  ['/jobs', 'Jobs', 'briefcase', 'j', 'backup schedule plan'],
  ['/sources', 'What’s protected', 'shield', 'w', 'sources databases folders volumes'],
  ['/places', 'Places', 'hard-drive', 'p', 'repositories connections clouds nas usb google drive b2'],
  ['/restore', 'Restore', 'rotate', 'r', 'snapshots files get back'],
  ['/runs', 'Runs', 'history', 'h', 'history log'],
  ['/coverage', 'Coverage', 'layers', 'c', 'containers apps not covered'],
  ['/backrest', 'Backrest', 'archive', 'b', 'plans hooks'],
]

const SETTINGS: [string, string, string, string][] = [
  ['general', 'Settings: General', 'settings', 'theme title'],
  ['notifications', 'Settings: Notifications', 'mail', 'apprise ntfy webhook'],
  ['integrations', 'Settings: Integrations', 'plug', 'homepage report backrest tools'],
  ['profile', 'Settings: Profile', 'user', 'password name'],
  ['tokens', 'Settings: Tokens', 'key', 'api hooks'],
  ['about', 'Settings: About', 'info', 'version core'],
]

export function Shell({ title, sub, controls, loading, extraCommands = [], children }: {
  title: React.ReactNode
  sub?: React.ReactNode
  controls?: React.ReactNode
  loading?: boolean
  extraCommands?: PaletteCommand[]
  children: React.ReactNode
}) {
  const app = useApp()
  const jobs = useJobs()
  const navigate = useNavigate()
  const { toast } = useToast()
  const mobile = useIsMobile()
  const narrow = useMediaQuery('(max-width: 1099px)')
  const drawer = mobile || narrow
  const [sidebarOpen, setSidebarOpen] = useState(() => !drawer && localStorage.getItem(SIDEBAR_KEY) !== '0')
  useEffect(() => { setSidebarOpen(!drawer && localStorage.getItem(SIDEBAR_KEY) !== '0') }, [drawer])
  const openedAt = useRef(0)
  const toggleSidebar = () => setSidebarOpen(o => { openedAt.current = performance.now(); const n = !o; if (!drawer) localStorage.setItem(SIDEBAR_KEY, n ? '1' : '0'); return n })
  const [palette, setPalette] = useState(false)
  const [shortcuts, setShortcuts] = useState(false)
  useEffect(() => {
    const h = () => setShortcuts(true)
    window.addEventListener('pbk:shortcuts', h)
    return () => window.removeEventListener('pbk:shortcuts', h)
  }, [])

  const runJob = async (id: number) => {
    try { const r = await api.post<Run>(`${V1}/jobs/${id}/run`); toast('Started'); app.bump(); navigate(`/runs/${r.id}`) } catch (e) { toast(errMsg(e), 'err') }
  }

  // "g" then a letter, like most dashboards.
  const pendingG = useRef(0)
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement
      const typing = t && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)
      const mod = e.metaKey || e.ctrlKey
      if (mod && e.key.toLowerCase() === 'k') { e.preventDefault(); if (!modalOpen()) setPalette(true); return }
      if (typing || modalOpen() || mod || e.altKey) return
      const k = e.key
      if (Date.now() - pendingG.current < 1200) {
        pendingG.current = 0
        const p = PAGES.find(x => x[3] === k)
        if (p) { navigate(p[0]); return }
        if (k === 's') { navigate('/settings'); return }
      }
      if (k === 'g') { pendingG.current = Date.now(); return }
      if (k === '/') { e.preventDefault(); setPalette(true) }
      else if (k === '?') setShortcuts(true)
      else if (k === 'n' && app.isAdmin) { e.preventDefault(); openJobEditor() }
    }
    window.addEventListener('keydown', h)
    return () => window.removeEventListener('keydown', h)
  })

  const admin = app.isAdmin
  const commands: PaletteCommand[] = [
    ...extraCommands,
    ...(admin ? [
      { id: 'new-job', label: 'New job', icon: 'plus', hint: 'N', keywords: 'add backup schedule', run: () => openJobEditor() },
      { id: 'new-source', label: 'Protect something new', icon: 'shield', keywords: 'add source database folder volume', run: () => openSourceEditor() },
      { id: 'new-repo', label: 'Add a repository', icon: 'hard-drive', keywords: 'restic usb disk', run: () => openRepoEditor() },
      { id: 'new-conn', label: 'Add a connection', icon: 'cloud', keywords: 'google drive onedrive dropbox b2 s3 sftp nas webdav pocket files', run: () => openConnEditor() },
      ...jobs.map(j => ({ id: 'run-' + j.id, label: `Run ${j.name}`, icon: 'play', group: 'Run now', keywords: 'start backup now', sub: j.engine === 'backrest' ? 'on Backrest' : undefined, run: () => runJob(j.id) })),
      ...jobs.map(j => ({ id: 'job-' + j.id, label: j.name, icon: 'briefcase', group: 'Jobs', keywords: 'job', run: () => navigate(`/jobs/${j.id}`) })),
    ] : []),
    ...(admin ? PAGES : PAGES.slice(0, 1)).map(([to, label, icon, , kw]) => ({ id: 'go' + to, label, icon, keywords: kw, group: 'Go to', run: () => navigate(to) })),
    ...SETTINGS.filter(([tab]) => admin || !['integrations'].includes(tab)).map(([tab, label, icon, kw]) => ({ id: 'set-' + tab, label, icon, keywords: kw, group: 'Settings', run: () => navigate(`/settings/${tab}`) })),
    ...(admin ? [{ id: 'set-people', label: 'Settings: People', icon: 'users', keywords: 'accounts users', group: 'Settings', run: () => navigate('/settings/people') }] : []),
    { id: 'theme', label: app.theme === 'dark' ? 'Switch to light mode' : 'Switch to dark mode', icon: app.theme === 'dark' ? 'sun' : 'moon', keywords: 'theme appearance', run: () => app.setThemeMode(app.theme === 'dark' ? 'light' : 'dark') },
    { id: 'shortcuts', label: 'Keyboard shortcuts', icon: 'keyboard', hint: '?', run: () => setShortcuts(true) },
  ]

  return (
    <div className={`app ${sidebarOpen ? 'with-sidebar' : 'no-sidebar'} ${mobile ? 'is-mobile' : ''} ${drawer ? 'drawer-mode' : ''}`}>
      {drawer && sidebarOpen && <div className="drawer-backdrop" onClick={e => { if (e.timeStamp - openedAt.current > 400) setSidebarOpen(false) }} />}
      <div className={`sidebar-wrap ${sidebarOpen ? 'open' : ''}`}>
        <Sidebar onNavigate={() => drawer && setSidebarOpen(false)} />
      </div>
      <main className="main">
        <TopBar title={title} sub={sub} controls={controls} onPalette={() => setPalette(true)} onMenu={toggleSidebar} mobile={mobile} compact={narrow} loading={loading} sidebarOpen={sidebarOpen} />
        <div className="page"><div className="page-inner">{children}</div></div>
      </main>
      {palette && <CommandPalette onClose={() => setPalette(false)} commands={commands} />}
      {shortcuts && <ShortcutsSheet onClose={() => setShortcuts(false)} />}
    </div>
  )
}
