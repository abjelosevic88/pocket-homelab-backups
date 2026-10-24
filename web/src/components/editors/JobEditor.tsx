import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { api, V1 } from '../../lib/api'
import { useApp } from '../../lib/app-context'
import { useData } from '../../lib/data'
import type { BackrestInfo, Job, JobConfig, PullConfig, Retention } from '../../lib/types'
import { Icon } from '../icons'
import { ListInput, NumInput, PathInput } from '../pickers'
import { Select } from '../Select'
import { errMsg, Field, Modal, Segmented, Spinner, useToast } from '../ui'
import { Check, FormSection, MultiPick, ScheduleInput } from './common'
import { KIND_NAMES } from '../../pages/sourceKinds'

type Draft = {
  name: string
  kind: Job['kind']
  engine: Job['engine']
  backrest_plan: string
  repository_id: number | null
  schedule: string
  enabled: boolean
  config: JobConfig
  source_ids: number[]
}

const emptyPull = (): PullConfig => ({ connection_id: 0, remote_path: '', local_path: '', export_formats: 'docx,xlsx,pptx,svg', require_mount: false })

export function JobEditor({ job, onClose }: { job?: Job; onClose: () => void }) {
  const { reload, repos, sources, conns } = useData()
  const { bump } = useApp()
  const { toast } = useToast()
  const navigate = useNavigate()
  const [br, setBr] = useState<BackrestInfo | null>(null)
  const [busy, setBusy] = useState(false)
  const [d, setD] = useState<Draft>(() => job
    ? { name: job.name, kind: job.kind, engine: job.engine, backrest_plan: job.backrest_plan, repository_id: job.repository_id, schedule: job.schedule, enabled: job.enabled, config: { ...job.config, retention: { ...job.config.retention } }, source_ids: [...job.source_ids] }
    : { name: '', kind: 'backup', engine: 'pocket', backrest_plan: '', repository_id: repos[0]?.id ?? null, schedule: '0 3 * * *', enabled: true,
      config: { retention: { keep_daily: 7, keep_weekly: 4, keep_monthly: 6, prune: true }, notify_failure: true, notify_success: false, verify_after: true }, source_ids: [] })
  useEffect(() => { api.get<BackrestInfo>(`${V1}/backrest`).then(setBr).catch(() => setBr(null)) }, [])

  const set = <K extends keyof Draft>(k: K, v: Draft[K]) => setD(x => ({ ...x, [k]: v }))
  const cfg = <K extends keyof JobConfig>(k: K, v: JobConfig[K]) => setD(x => ({ ...x, config: { ...x.config, [k]: v } }))
  const ret = <K extends keyof Retention>(k: K, v: Retention[K]) => setD(x => ({ ...x, config: { ...x.config, retention: { ...x.config.retention, [k]: v } } }))
  const pull = d.config.pull || emptyPull()
  const setPull = <K extends keyof PullConfig>(k: K, v: PullConfig[K]) => setD(x => ({ ...x, config: { ...x.config, pull: { ...(x.config.pull || emptyPull()), [k]: v } } }))
  const brReady = !!br?.configured && !br.error
  const plan = br?.plans.find(p => p.id === d.backrest_plan)

  const save = async () => {
    setBusy(true)
    try {
      const body = { ...d, config: { ...d.config, pull: d.kind === 'pull' ? pull : null }, repository_id: d.kind === 'pull' ? -1 : d.repository_id ?? -1,
        schedule: d.engine === 'backrest' && d.kind === 'backup' ? (plan?.cron ?? d.schedule) : d.schedule }
      const saved = job ? await api.put<Job>(`${V1}/jobs/${job.id}`, body) : await api.post<Job>(`${V1}/jobs`, body)
      toast(job ? 'Job saved' : 'Job created')
      await reload(); bump()
      onClose()
      if (!job) navigate(`/jobs/${saved.id}`)
    } catch (e) { toast(errMsg(e), 'err') } finally { setBusy(false) }
  }

  const r = d.config.retention
  return (
    <Modal title={job ? `Edit ${job.name}` : 'New job'} onClose={onClose} size="xl" footer={<>
      <button className="btn" onClick={onClose}>Cancel</button>
      <button className="btn primary" disabled={busy || !d.name.trim()} onClick={save}>{busy && <Spinner />}{job ? 'Save' : 'Create job'}</button>
    </>}>
      <div className="stack">
        <div className="two-col">
          <Field label="Name"><input value={d.name} onChange={e => set('name', e.target.value)} placeholder={d.kind === 'pull' ? 'Google Drive to the NAS' : 'Nightly backup'} autoFocus /></Field>
          <Field label="What it does" group>
            <Segmented value={d.kind} onChange={v => set('kind', v)} label="Kind" options={[{ value: 'backup', label: 'Back up this box' }, { value: 'pull', label: 'Bring a cloud folder home' }]} />
          </Field>
        </div>

        {d.kind === 'backup' && (
          <FormSection title="Who makes the snapshot" sub="Pocket Backups can do it on its own, or let Backrest run the snapshot on its schedule while this app does everything before and after.">
            <Segmented value={d.engine} onChange={v => set('engine', v)} label="Engine" options={[
              { value: 'pocket', label: <span className="row"><Icon name="shield" size={14} />Pocket Backups</span> },
              { value: 'backrest', label: <span className="row"><Icon name="archive" size={14} />Backrest</span>, title: brReady ? '' : 'No Backrest is configured' },
            ]} />
            {d.engine === 'backrest' && !brReady && <div className="callout warn"><Icon name="alert" size={15} /><span>{br?.error ? `Backrest does not answer: ${br.error}` : 'No Backrest is configured on this box. Set BACKREST_URL (and BACKREST_USER / BACKREST_PASSWORD) for Pocket Backups, or let Pocket Backups make the snapshots itself.'}</span></div>}
            {d.engine === 'backrest' && brReady && (
              <Field label="Backrest plan" help={plan ? `Runs ${plan.cron || 'by hand'} on Backrest · ${plan.paths.length} folder(s) · repository ${plan.repo}` : 'Its hooks call this app — install them on the Backrest page.'}>
                <Select value={d.backrest_plan} onChange={v => set('backrest_plan', v)} placeholder="Choose a plan…" options={(br?.plans || []).map(p => ({ value: p.id, label: p.id, hint: p.repo }))} />
              </Field>
            )}
          </FormSection>
        )}

        {d.kind === 'backup' ? (
          <FormSection title="Where it goes" sub="The repository that keeps the snapshots. Copies to more places are added on the job page.">
            <Select value={d.repository_id ?? 0} onChange={v => set('repository_id', v || null)} placeholder="Choose a repository…"
              options={repos.map(x => ({ value: x.id, label: x.name, hint: x.location === 'usb' ? 'USB disk' : x.location }))} />
            {!repos.length && <span className="muted small">No repository yet — add one under Places first.</span>}
          </FormSection>
        ) : (
          <FormSection title="Which cloud folder" sub="Copies a folder of a connection to a folder on this box. Copy never deletes anything here; mirror does.">
            <Field label="Connection">
              <Select value={pull.connection_id} onChange={v => setPull('connection_id', v)} placeholder="Choose a connection…" options={conns.map(c => ({ value: c.id, label: c.name, hint: c.kind }))} />
            </Field>
            <div className="two-col">
              <Field label="Folder in the cloud" help="Empty = everything."><PathInput value={pull.remote_path} onChange={v => setPull('remote_path', v)} connection={pull.connection_id || null} placeholder="(top)" /></Field>
              <Field label="Folder on this box"><PathInput value={pull.local_path} onChange={v => setPull('local_path', v)} placeholder="/mnt/gdrive/drive" /></Field>
            </div>
            <Check checked={pull.mirror} onChange={v => setPull('mirror', v)} label="Mirror" help="Also delete files here that were deleted in the cloud. Off is safer: nothing here is ever deleted." />
            <Check checked={pull.require_mount} onChange={v => setPull('require_mount', v)} label="The folder above must be on a mounted share" help="Refuses to run if the NAS share is not mounted, instead of filling the local disk." />
            {conns.find(c => c.id === pull.connection_id)?.kind === 'gdrive' && <>
              <Check checked={pull.shared_with_me} onChange={v => setPull('shared_with_me', v)} label="Also bring “Shared with me”" />
              {pull.shared_with_me && <Field label="“Shared with me” goes to"><PathInput value={pull.shared_path || ''} onChange={v => setPull('shared_path', v)} placeholder="/mnt/gdrive/shared-with-me" /></Field>}
              <Field label="Google Docs, Sheets and Slides become" help="Comma-separated file types, e.g. docx,xlsx,pptx,svg."><input className="mono" value={pull.export_formats || ''} onChange={e => setPull('export_formats', e.target.value)} /></Field>
            </>}
            <div className="row wrap">
              <Field label="Transfers at once"><NumInput value={pull.transfers} onChange={v => setPull('transfers', v)} placeholder="8" /></Field>
              <Field label="Requests per second" help="0 = no limit"><NumInput value={pull.tps_limit} onChange={v => setPull('tps_limit', v)} placeholder="10" /></Field>
            </div>
            <Field label="Extra rclone flags" help="One per line, for experts."><ListInput value={pull.extra_flags} onChange={v => setPull('extra_flags', v)} rows={2} /></Field>
          </FormSection>
        )}

        {d.kind === 'backup' && (
          <FormSection title="What’s included" sub={d.engine === 'backrest' ? 'The Backrest plan backs up its own folders; these are the dumps and exports this app makes before each snapshot.' : 'The sources this job captures, plus any extra folders.'}>
            <MultiPick value={d.source_ids} onChange={v => set('source_ids', v)} empty="No sources yet — add them under What’s protected."
              options={sources.map(s => ({ value: s.id, label: s.name, sub: KIND_NAMES[s.kind] }))} />
            {d.engine === 'pocket' && <div className="two-col">
              <Field label="Extra folders" help="One per line, absolute paths."><ListInput value={d.config.paths} onChange={v => cfg('paths', v)} placeholder="/home/me/documents" /></Field>
              <Field label="Leave out" help="Patterns restic understands, one per line."><ListInput value={d.config.excludes} onChange={v => cfg('excludes', v)} placeholder="**/cache" /></Field>
            </div>}
            <Field label="Staging folder" help="Where dumps are written before the snapshot. Empty = a folder of its own inside the app’s data."><PathInput value={d.config.staging_dir || ''} onChange={v => cfg('staging_dir', v)} placeholder="(automatic)" /></Field>
            <Field label="Folders that must be mounted" help="A NAS share that is not mounted looks like an empty folder; the job refuses to run instead."><ListInput value={d.config.require_mounts} onChange={v => cfg('require_mounts', v)} rows={2} placeholder="/home/me/media" /></Field>
            {d.engine === 'pocket' && <Check checked={d.config.exclude_caches} onChange={v => cfg('exclude_caches', v)} label="Skip cache folders" help="Folders marked with a CACHEDIR.TAG file." />}
            {d.engine === 'pocket' && <Field label="Tags" help="Extra labels on each snapshot, one per line."><ListInput value={d.config.tags} onChange={v => cfg('tags', v)} rows={2} /></Field>}
          </FormSection>
        )}

        <FormSection title="When" sub={d.engine === 'backrest' && d.kind === 'backup' ? 'Backrest decides when the plan runs.' : undefined}>
          {d.engine === 'backrest' && d.kind === 'backup'
            ? <span className="muted small">{plan ? (plan.cron || 'By hand') : 'Choose a plan above.'}</span>
            : <ScheduleInput value={d.schedule} onChange={v => set('schedule', v)} />}
          <Check checked={d.enabled} onChange={v => set('enabled', v)} label="Switched on" help="Off = never runs on its schedule (you can still start it by hand)." />
          {d.kind === 'backup' && <>
            <Check checked={d.config.run_on_connect} onChange={v => cfg('run_on_connect', v)} label="Start when its USB disk is plugged in" help="For an offline disk you connect now and then." />
            {d.config.run_on_connect && <Field label="…but only if the last good run is at least this many days old"><NumInput value={d.config.run_on_connect_days} onChange={v => cfg('run_on_connect_days', v)} placeholder="0" /></Field>}
          </>}
        </FormSection>

        {d.kind === 'backup' && (
          <FormSection title="How long snapshots are kept" sub="Old snapshots are forgotten after each run. Leave everything empty to keep them all.">
            <Check checked={r.keep_all} onChange={v => ret('keep_all', v)} label="Keep every snapshot" />
            {!r.keep_all && <div className="ret-grid">
              {([['keep_last', 'Newest'], ['keep_hourly', 'Hourly'], ['keep_daily', 'Daily'], ['keep_weekly', 'Weekly'], ['keep_monthly', 'Monthly'], ['keep_yearly', 'Yearly']] as [keyof Retention, string][]).map(([k, l]) => (
                <Field key={k} label={l}><NumInput value={r[k] as number | undefined} onChange={v => ret(k, v)} placeholder="–" width={80} /></Field>
              ))}
            </div>}
            <Check checked={r.prune} onChange={v => ret('prune', v)} label="Free the space right away (prune)" help="Removes the data of forgotten snapshots in the same run." />
            <Check checked={r.group_by_none} onChange={v => ret('group_by_none', v)} label="Count all snapshots together" help="restic --group-by '': otherwise snapshots are counted separately per folder set." />
            <Check checked={d.config.forget_before_copies} onChange={v => cfg('forget_before_copies', v)} label="Apply this before the copies" help="So the copies carry only what is kept (small cloud quotas)." />
          </FormSection>
        )}

        <FormSection title="Checks, reports and telling people">
          <Check checked={d.config.notify_failure} onChange={v => cfg('notify_failure', v)} label="Tell me when it fails or warns" />
          <Check checked={d.config.notify_success} onChange={v => cfg('notify_success', v)} label="Tell me when it went well" />
          {d.kind === 'backup' && <>
            <Check checked={d.config.verify_after} onChange={v => cfg('verify_after', v)} label="Read the repository back after each snapshot" help="Proves the new snapshot is really there." />
            <Check checked={d.config.coverage_check} onChange={v => cfg('coverage_check', v)} label="Warn about apps no backup covers" help="Checks the running containers before each run." />
            <Check checked={d.config.report} onChange={v => cfg('report', v)} label="Update the Homepage report" help="Files set under Settings → Integrations." />
            <Field label="Check the repository after each run" help="Reads this share of the data back (0 = off). Good for an offline disk."><NumInput value={d.config.check_read_pct} onChange={v => cfg('check_read_pct', v)} max={100} placeholder="0" /> </Field>
          </>}
          <Field label="Heartbeat address" help="Opened after every good run — an Uptime Kuma push monitor alerts you when it stops coming."><input className="mono" value={d.config.heartbeat_url || ''} onChange={e => cfg('heartbeat_url', e.target.value)} placeholder="https://status.example.com/api/push/…?status=up" /></Field>
        </FormSection>
      </div>
    </Modal>
  )
}
