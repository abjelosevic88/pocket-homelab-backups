import type { SourceKind } from '../lib/types'

export const KIND_ORDER: SourceKind[] = ['paths', 'sqlite', 'postgres', 'mariadb', 'volume', 'container_files', 'container_command', 'host_command']

export const KIND_NAMES: Record<SourceKind, string> = {
  paths: 'Folders',
  sqlite: 'SQLite databases',
  postgres: 'PostgreSQL',
  mariadb: 'MariaDB / MySQL',
  volume: 'Docker volumes',
  container_files: 'Files from a container',
  container_command: 'Command in a container',
  host_command: 'Command on the host',
}

export const KIND_ONE: Record<SourceKind, string> = {
  paths: 'Folders', sqlite: 'SQLite database', postgres: 'PostgreSQL database', mariadb: 'MariaDB / MySQL database', volume: 'Docker volume',
  container_files: 'Files from a container', container_command: 'Command in a container', host_command: 'Command on the host',
}

export const KIND_ICON: Record<SourceKind, string> = {
  paths: 'folder', sqlite: 'database', postgres: 'database', mariadb: 'database', volume: 'box',
  container_files: 'container', container_command: 'terminal', host_command: 'terminal',
}

export const KIND_HELP: Record<SourceKind, string> = {
  paths: 'Folders on this box, backed up as they are. Good for files that are not being written while the backup runs.',
  sqlite: 'A database file an app keeps open. It is copied consistently (never half-written) before each snapshot.',
  postgres: 'A PostgreSQL database in a container, dumped with pg_dump before each snapshot.',
  mariadb: 'A MariaDB or MySQL database in a container, dumped with mariadb-dump before each snapshot.',
  volume: 'A named Docker volume, exported as a compressed archive before each snapshot.',
  container_files: 'Files inside a running container (keys, configuration only readable there), packed into an archive.',
  container_command: 'The output of a command run inside a container, saved to a file.',
  host_command: 'The output of a command run on the host itself (system state, package lists). Needs host access.',
}
