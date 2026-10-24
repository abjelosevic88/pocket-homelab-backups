import React, { useEffect, useState } from 'react'
import { cronText } from '../../lib/format'
import { Select } from '../Select'
import { Segmented } from '../ui'

/** A titled block inside an editor. */
export function FormSection({ title, sub, children }: { title: string; sub?: React.ReactNode; children: React.ReactNode }) {
  return (
    <section className="form-sec">
      <div className="form-sec-head"><h3>{title}</h3>{sub && <p className="muted small">{sub}</p>}</div>
      <div className="stack tight">{children}</div>
    </section>
  )
}

/** A checkbox row with an explanation. */
export function Check({ checked, onChange, label, help, disabled }: { checked?: boolean; onChange: (v: boolean) => void; label: React.ReactNode; help?: React.ReactNode; disabled?: boolean }) {
  return (
    <label className={`check-row ${disabled ? 'disabled' : ''}`}>
      <input type="checkbox" checked={!!checked} disabled={disabled} onChange={e => onChange(e.target.checked)} />
      <span><span className="check-label">{label}</span>{help && <span className="check-help">{help}</span>}</span>
    </label>
  )
}

type Preset = 'manual' | 'daily' | 'weekly' | 'monthly' | 'custom'
const DAYS = ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']

function parse(c: string): { preset: Preset; time: string; day: number } {
  const s = c.trim()
  if (!s) return { preset: 'manual', time: '03:00', day: 0 }
  const f = s.split(/\s+/)
  if (f.length === 5 && /^\d+$/.test(f[0]) && /^\d+$/.test(f[1])) {
    const time = `${f[1].padStart(2, '0')}:${f[0].padStart(2, '0')}`
    if (f[2] === '*' && f[3] === '*' && f[4] === '*') return { preset: 'daily', time, day: 0 }
    if (f[2] === '*' && f[3] === '*' && /^\d$/.test(f[4])) return { preset: 'weekly', time, day: +f[4] % 7 }
    if (f[2] === '1' && f[3] === '*' && f[4] === '*') return { preset: 'monthly', time, day: 0 }
  }
  return { preset: 'custom', time: '03:00', day: 0 }
}

function build(p: Preset, time: string, day: number, custom: string): string {
  const [h, m] = (time || '03:00').split(':').map(n => parseInt(n, 10) || 0)
  switch (p) {
    case 'manual': return ''
    case 'daily': return `${m} ${h} * * *`
    case 'weekly': return `${m} ${h} * * ${day}`
    case 'monthly': return `${m} ${h} 1 * *`
    default: return custom
  }
}

/** When something runs: by hand, daily, weekly, monthly or a cron line. */
export function ScheduleInput({ value, onChange, manualLabel = 'By hand' }: { value: string; onChange: (v: string) => void; manualLabel?: string }) {
  const init = parse(value)
  const [preset, setPreset] = useState<Preset>(init.preset)
  const [time, setTime] = useState(init.time)
  const [day, setDay] = useState(init.day)
  const [custom, setCustom] = useState(value)
  useEffect(() => { onChange(build(preset, time, day, custom)) }, [preset, time, day, custom]) // eslint-disable-line react-hooks/exhaustive-deps
  return (
    <div className="stack tight">
      <Segmented value={preset} onChange={v => { if (v === 'custom') setCustom(build(preset, time, day, custom) || '0 3 * * *'); setPreset(v) }} size="sm" label="Schedule" options={[
        { value: 'manual', label: manualLabel }, { value: 'daily', label: 'Daily' }, { value: 'weekly', label: 'Weekly' }, { value: 'monthly', label: 'Monthly' }, { value: 'custom', label: 'Custom' },
      ]} />
      {(preset === 'daily' || preset === 'weekly' || preset === 'monthly') && (
        <div className="row wrap">
          {preset === 'weekly' && <Select value={day} onChange={setDay} options={DAYS.map((d, i) => ({ value: i, label: d }))} label="Day" size="sm" />}
          {preset === 'monthly' && <span className="muted small">On the 1st, at</span>}
          <input type="time" value={time} onChange={e => setTime(e.target.value)} style={{ width: 120 }} aria-label="Time" />
        </div>
      )}
      {preset === 'custom' && <input className="mono" value={custom} onChange={e => setCustom(e.target.value)} placeholder="minute hour day month weekday — e.g. 15 4 * * *" />}
      <span className="muted small">{cronText(build(preset, time, day, custom))}</span>
    </div>
  )
}

/** Picks several ids from a list with checkboxes. */
export function MultiPick<T extends number>({ options, value, onChange, empty }: { options: { value: T; label: string; sub?: string }[]; value: T[]; onChange: (v: T[]) => void; empty?: string }) {
  if (!options.length) return <span className="muted small">{empty || 'Nothing to choose from yet.'}</span>
  return (
    <div className="multi-pick">
      {options.map(o => (
        <label key={o.value} className="multi-row">
          <input type="checkbox" checked={value.includes(o.value)} onChange={e => onChange(e.target.checked ? [...value, o.value] : value.filter(v => v !== o.value))} />
          <span className="grow ellipsis">{o.label}</span>
          {o.sub && <span className="muted small ellipsis">{o.sub}</span>}
        </label>
      ))}
    </div>
  )
}
