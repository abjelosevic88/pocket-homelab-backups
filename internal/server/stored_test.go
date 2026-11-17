package server

import (
	"testing"

	"github.com/abjelosevic88/pocket-homelab-backups/internal/store"
)

func TestURIOnConn(t *testing.T) {
	c := &store.Connection{Kind: "sftp", Config: store.ConnConfig{Host: "192.168.100.239", User: "me"}}
	for path, want := range map[string]bool{
		"sftp:me@192.168.100.239:/mnt/tank/repo":  true,
		"sftp:192.168.100.239:/mnt/tank/repo":     true,
		"sftp://me@192.168.100.239/mnt/tank/repo": true,
		"sftp:you@192.168.100.239:/repo":          false,
		"sftp:me@10.0.0.1:/repo":                  false,
		"s3:s3.amazonaws.com/bucket":              false,
	} {
		if got := uriOnConn(&store.Repository{Location: "uri", Path: path}, c); got != want {
			t.Errorf("%s: got %v, want %v", path, got, want)
		}
	}
}
