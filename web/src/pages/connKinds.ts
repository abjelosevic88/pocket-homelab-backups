import type { ConnKindId } from '../lib/types'

export const CONN_ICON: Record<ConnKindId, string> = {
  gdrive: 'cloud', onedrive: 'cloud', dropbox: 'box', b2: 'database', s3: 'database', sftp: 'server', webdav: 'globe', pocketfiles: 'folder', rclone: 'terminal',
}

export const CONN_NAME: Record<ConnKindId, string> = {
  gdrive: 'Google Drive', onedrive: 'OneDrive', dropbox: 'Dropbox', b2: 'Backblaze B2', s3: 'S3 storage', sftp: 'SFTP server', webdav: 'WebDAV', pocketfiles: 'Pocket Files', rclone: 'rclone remote',
}

export const SIGN_IN: Partial<Record<ConnKindId, string>> = { gdrive: 'Sign in with Google', onedrive: 'Sign in with Microsoft', dropbox: 'Sign in with Dropbox' }

export const CORE_HINT = 'Needs Pocket Core. Pocket Core connects your Pocket apps to each other and to your services: one sign-in for everyone, each person’s own accounts, nothing to copy or paste. Set CORE_URL for Pocket Backups.'
export const CORE_LINK = 'https://github.com/abjelosevic88/pocket-homelab-core'

/** Config keys a form field writes (everything else is a secret). */
export const CONFIG_KEYS = ['host', 'port', 'user', 'key_file', 'url', 'vendor', 'bucket', 'endpoint', 'region', 'provider', 'account', 'root', 'scope']
