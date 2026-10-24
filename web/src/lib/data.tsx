// The box's backup setup, loaded once and refreshed whenever the server says
// something changed (jobs, places, sources, connections). Admins only.
import React, { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react'
import { api, V1 } from './api'
import { useApp } from './app-context'
import type { Connection, Job, Repository, Source } from './types'

interface Data {
  jobs: Job[]
  repos: Repository[]
  sources: Source[]
  conns: Connection[]
  loaded: boolean
  reload: () => Promise<void>
}

const Ctx = createContext<Data>({ jobs: [], repos: [], sources: [], conns: [], loaded: false, reload: async () => {} })

export function DataProvider({ children }: { children: React.ReactNode }) {
  const { isAdmin, version, user } = useApp()
  const [jobs, setJobs] = useState<Job[]>([])
  const [repos, setRepos] = useState<Repository[]>([])
  const [sources, setSources] = useState<Source[]>([])
  const [conns, setConns] = useState<Connection[]>([])
  const [loaded, setLoaded] = useState(false)
  const reload = useCallback(async () => {
    if (!isAdmin) { setLoaded(true); return }
    const [j, r, s, c] = await Promise.all([
      api.get<Job[]>(`${V1}/jobs`).catch(() => null),
      api.get<Repository[]>(`${V1}/repositories`).catch(() => null),
      api.get<Source[]>(`${V1}/sources`).catch(() => null),
      api.get<Connection[]>(`${V1}/connections`).catch(() => null),
    ])
    if (j) setJobs(j)
    if (r) setRepos(r)
    if (s) setSources(s)
    if (c) setConns(c)
    setLoaded(true)
  }, [isAdmin])
  useEffect(() => { if (user) reload() }, [user, version, reload])
  const value = useMemo(() => ({ jobs, repos, sources, conns, loaded, reload }), [jobs, repos, sources, conns, loaded, reload])
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>
}

export const useData = () => useContext(Ctx)
export const useJobs = () => useContext(Ctx).jobs
export const useRepos = () => useContext(Ctx).repos
export const useSources = () => useContext(Ctx).sources
export const useConns = () => useContext(Ctx).conns
