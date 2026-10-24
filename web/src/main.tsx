import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import './styles/base.css'
import './styles/app.css'
import './styles/backups.css'
import './styles/settings.css'
import { AppProvider, useApp } from './lib/app-context'
import { DataProvider } from './lib/data'
import { Loading, ToastProvider } from './components/ui'
import { ErrorBoundary } from './components/ErrorBoundary'
import { DialogHost } from './components/dialogs'
import Login from './pages/Login'
import Overview from './pages/Overview'
import Jobs from './pages/Jobs'
import JobPage from './pages/JobPage'
import Sources from './pages/Sources'
import Places from './pages/Places'
import RepoPage from './pages/RepoPage'
import Restore from './pages/Restore'
import Runs from './pages/Runs'
import Schedule from './pages/Schedule'
import RunPage from './pages/RunPage'
import Coverage from './pages/Coverage'
import Backrest from './pages/Backrest'
import Settings from './pages/Settings'

function Gate({ children, admin }: { children: React.ReactNode; admin?: boolean }) {
  const { status, user, settings, isAdmin } = useApp()
  const loc = useLocation()
  if (!status) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (status.needs_setup) return <Navigate to="/setup" replace />
  if (!user) return <Navigate to="/login" replace state={{ from: loc.pathname + loc.search }} />
  if (!settings) return <div className="boot"><img src="/favicon.svg" alt="" /><Loading /></div>
  if (admin && !isAdmin) return <Navigate to="/" replace />
  return <>{children}</>
}

const page = (where: string, el: React.ReactNode, admin = true) => <Gate admin={admin}><ErrorBoundary where={where}>{el}</ErrorBoundary></Gate>

function App() {
  return (
    <>
      <Routes>
        <Route path="/login" element={<Login mode="login" />} />
        <Route path="/setup" element={<Login mode="setup" />} />
        <Route path="/settings/:tab?" element={page('settings', <Settings />, false)} />
        <Route path="/jobs" element={page('jobs', <Jobs />)} />
        <Route path="/jobs/:id" element={page('job', <JobPage />)} />
        <Route path="/sources" element={page('sources', <Sources />)} />
        <Route path="/places" element={page('places', <Places />)} />
        <Route path="/places/repositories/:id" element={page('repository', <RepoPage />)} />
        <Route path="/restore" element={page('restore', <Restore />)} />
        <Route path="/schedule" element={page('schedule', <Schedule />)} />
        <Route path="/runs" element={page('runs', <Runs />)} />
        <Route path="/runs/:id" element={page('run', <RunPage />)} />
        <Route path="/coverage" element={page('coverage', <Coverage />)} />
        <Route path="/backrest" element={page('backrest', <Backrest />)} />
        <Route path="/" element={page('overview', <Overview />, false)} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
      <DialogHost />
    </>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <BrowserRouter>
      <ErrorBoundary where="app"><ToastProvider><AppProvider><DataProvider><App /></DataProvider></AppProvider></ToastProvider></ErrorBoundary>
    </BrowserRouter>
  </React.StrictMode>,
)
