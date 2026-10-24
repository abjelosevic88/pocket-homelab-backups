import { Modal } from './ui'

const GROUPS: { title: string; keys: [string[], string][] }[] = [
  { title: 'Navigate', keys: [[['g', 'o'], 'Overview'], [['g', 'j'], 'Jobs'], [['g', 'w'], 'What’s protected'], [['g', 'p'], 'Places'], [['g', 'r'], 'Restore'], [['g', 'h'], 'Runs (history)'], [['g', 'c'], 'Coverage'], [['g', 's'], 'Settings']] },
  { title: 'Anywhere', keys: [[['/'], 'Search & commands'], [['⌘', 'K'], 'Search & commands'], [['n'], 'New job'], [['?'], 'This sheet']] },
]

export function ShortcutsSheet({ onClose }: { onClose: () => void }) {
  return (
    <Modal title="Keyboard shortcuts" onClose={onClose} size="lg">
      <div className="shortcuts">
        {GROUPS.map(g => (
          <section key={g.title}>
            <h4 className="sc-h">{g.title}</h4>
            {g.keys.map(([keys, label]) => (
              <div key={label + keys.join('')} className="sc-row"><span>{label}</span><span className="sc-keys">{keys.map(k => <kbd key={k}>{k}</kbd>)}</span></div>
            ))}
          </section>
        ))}
      </div>
    </Modal>
  )
}
