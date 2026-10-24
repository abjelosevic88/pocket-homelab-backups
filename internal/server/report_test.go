package server

import "testing"

func TestServiceOf(t *testing.T) {
	base, staging := "/home/u", "/home/u/backups/staging"
	cases := map[string]string{
		"/home/u/arr/sonarr/config.xml":                       "arr",
		"/home/u/backups/staging/db/firefly.sql":              "firefly-iii",
		"/home/u/backups/staging/db/pocket-money-db.sqlite3":  "pocket-money",
		"/home/u/backups/staging/volumes/n8n_n8n_data.tar.gz": "n8n",
		"/home/u/backups/staging/system/etc.tar.gz":           "system state",
		"/home/u/backups/staging/nextcloud/nextcloud.sql":     "nextcloud",
		"/home/u/.config/systemd/user/backrest.service":       "systemd units",
	}
	for p, want := range cases {
		if got := serviceOf(p, base, staging); got != want {
			t.Errorf("%s → %q, want %q", p, got, want)
		}
	}
	if b := reportBase([]string{"/home/u/a", "/home/u/b", "/mnt/x/y"}); b != "/home/u" {
		t.Errorf("base = %s", b)
	}
}
