import { useEffect, useState } from 'react'
import { useApp } from '../../lib/app-context'
import { Icon } from '../../components/icons'
import { Loading, Segmented, SettingRow } from '../../components/ui'
import { Section, useSaveSettings } from './common'

export function GeneralTab() {
  const { sr, settings, isAdmin, themeMode, setThemeMode } = useApp()
  const save = useSaveSettings()
  const [title, setTitle] = useState(settings?.title || '')
  useEffect(() => { setTitle(settings?.title || '') }, [settings?.title])
  if (!sr || !settings) return <Loading />
  const s = settings

  return (
    <div className="stack loose">
      <Section title="Appearance">
        <SettingRow title="Theme" help="System follows your device’s light or dark setting.">
          <Segmented label="Theme" value={themeMode} onChange={setThemeMode} options={[
            { value: 'system', label: <span className="row"><Icon name="monitor" size={14} />System</span> },
            { value: 'light', label: <span className="row"><Icon name="sun" size={14} />Light</span> },
            { value: 'dark', label: <span className="row"><Icon name="moon" size={14} />Dark</span> },
          ]} />
        </SettingRow>
        <SettingRow title="Density" help="Compact fits more rows on the screen.">
          <Segmented label="Density" value={s.density} onChange={v => save({ density: v })} options={[{ value: 'comfortable', label: 'Comfortable' }, { value: 'compact', label: 'Compact' }]} />
        </SettingRow>
        <SettingRow title="Time format">
          <Segmented label="Time format" value={s.time_format} onChange={v => save({ time_format: v })} options={[{ value: '24h', label: '13:00' }, { value: '12h', label: '1:00 PM' }]} />
        </SettingRow>
      </Section>

      {isAdmin && (
        <Section title="This box" sub="Only admins see this.">
          <SettingRow title="App title" help="Shown in the browser tab and on the sign-in page.">
            <input value={title} onChange={e => setTitle(e.target.value)} onBlur={() => { if (title.trim() && title !== s.title) save({ title: title.trim() }, 'Title saved') }}
              onKeyDown={e => { if (e.key === 'Enter') (e.target as HTMLInputElement).blur() }} style={{ width: 240 }} aria-label="App title" />
          </SettingRow>
        </Section>
      )}
    </div>
  )
}
