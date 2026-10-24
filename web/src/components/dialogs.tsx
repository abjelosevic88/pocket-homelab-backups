// The editors open from anywhere (sidebar, palette, pages) through one host.
import { useEffect, useState } from 'react'
import type { ConnKindId, Connection, Copy, Job, Repository, Source, SourceConfig, SourceKind } from '../lib/types'
import { JobEditor } from './editors/JobEditor'
import { SourceEditor } from './editors/SourceEditor'
import { RepoEditor } from './editors/RepoEditor'
import { ConnEditor } from './editors/ConnEditor'
import { CopyEditor } from './editors/CopyEditor'

export type SourcePrefill = { name?: string; kind?: SourceKind; project?: string; config?: SourceConfig; jobs?: number[] }

type Req =
  | { type: 'job'; job?: Job }
  | { type: 'source'; source?: Source; prefill?: SourcePrefill }
  | { type: 'repo'; repo?: Repository }
  | { type: 'conn'; conn?: Connection; kind?: ConnKindId }
  | { type: 'copy'; job: Job; copy?: Copy }

export function openDialog(r: Req) { window.dispatchEvent(new CustomEvent<Req>('pbk:dialog', { detail: r })) }
export const openJobEditor = (job?: Job) => openDialog({ type: 'job', job })
export const openSourceEditor = (source?: Source, prefill?: SourcePrefill) => openDialog({ type: 'source', source, prefill })
export const openRepoEditor = (repo?: Repository) => openDialog({ type: 'repo', repo })
export const openConnEditor = (conn?: Connection, kind?: ConnKindId) => openDialog({ type: 'conn', conn, kind })
export const openCopyEditor = (job: Job, copy?: Copy) => openDialog({ type: 'copy', job, copy })

export function DialogHost() {
  const [req, setReq] = useState<Req | null>(null)
  useEffect(() => {
    const h = (e: Event) => setReq((e as CustomEvent<Req>).detail)
    window.addEventListener('pbk:dialog', h)
    return () => window.removeEventListener('pbk:dialog', h)
  }, [])
  if (!req) return null
  const close = () => setReq(null)
  switch (req.type) {
    case 'job': return <JobEditor job={req.job} onClose={close} />
    case 'source': return <SourceEditor source={req.source} prefill={req.prefill} onClose={close} />
    case 'repo': return <RepoEditor repo={req.repo} onClose={close} />
    case 'conn': return <ConnEditor conn={req.conn} kind={req.kind} onClose={close} />
    case 'copy': return <CopyEditor job={req.job} copy={req.copy} onClose={close} />
  }
}
