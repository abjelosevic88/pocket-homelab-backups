import { useCallback } from 'react'
import { useNavigate } from 'react-router-dom'
import { errMsg, useToast } from '../components/ui'
import { api, V1 } from './api'
import { useApp } from './app-context'
import { useData } from './data'
import type { Repository, Run } from './types'

export function useActions() {
  const { toast } = useToast()
  const { bump } = useApp()
  const { reload } = useData()
  const navigate = useNavigate()

  const runJob = useCallback(async (id: number, open = false) => {
    try {
      const r = await api.post<Run>(`${V1}/jobs/${id}/run`)
      toast('Started')
      bump(); reload()
      if (open) navigate(`/runs/${r.id}`)
      return r
    } catch (e) { toast(errMsg(e), 'err') }
  }, [toast, bump, reload, navigate])

  const repoAction = useCallback(async (repo: Repository, action: string, readPct?: number) => {
    try {
      const r = await api.post<Run | { ok: boolean }>(`${V1}/repositories/${repo.id}/action`, { action, read_pct: readPct || 0 })
      const msg: Record<string, string> = { mount: 'Disk mounted', unmount: 'Disk unmounted — safe to unplug', resume: 'Copies resumed', pause: 'Copies paused' }
      toast(msg[action] || 'Started')
      bump(); reload()
      return r
    } catch (e) { toast(errMsg(e), 'err') }
  }, [toast, bump, reload])

  return { runJob, repoAction }
}
