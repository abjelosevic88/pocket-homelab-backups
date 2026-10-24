package engine

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// USB is a removable disk a repository lives on.
type USB struct {
	UUID       string
	Label      string
	Mountpoint string
	PowerOff   bool
}

// ErrDiskAbsent is returned when the disk is not plugged in.
var ErrDiskAbsent = errors.New("the disk is not connected")

// USBConnected reports whether the disk's filesystem is visible to the host.
func (e *Engine) USBConnected(uuid string) bool {
	if uuid == "" {
		return false
	}
	for _, dir := range []string{e.DevDiskDir, "/dev/disk"} {
		if dir == "" {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, "by-uuid", uuid)); err == nil {
			return true
		}
	}
	return false
}

// Mounted reports whether a path is a mount point as this process sees it.
func Mounted(path string) bool {
	var st, parent syscall.Stat_t
	if syscall.Stat(path, &st) != nil || syscall.Stat(filepath.Dir(strings.TrimRight(path, "/")), &parent) != nil {
		return false
	}
	if st.Dev != parent.Dev {
		return true
	}
	// a bind mount of the same filesystem: ask /proc
	b, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return false
	}
	clean := filepath.Clean(path)
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		if len(f) > 4 && unescapeMount(f[4]) == clean {
			return true
		}
	}
	return false
}

func unescapeMount(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// MountUSB mounts the disk on the host (the fstab entry when there is one)
// and checks it is the right filesystem before anything writes to it.
func (e *Engine) MountUSB(ctx context.Context, d USB, log *Log) error {
	if d.UUID == "" || d.Mountpoint == "" {
		return errors.New("the disk needs a filesystem UUID and a mount point")
	}
	if !e.USBConnected(d.UUID) && e.DevDiskDir != "" {
		return ErrDiskAbsent
	}
	script := fmt.Sprintf(`set -u
DEV=/dev/disk/by-uuid/%[1]s; MP=%[2]s; LABEL=%[3]s
[ -e "$DEV" ] || { echo "the disk is not connected" >&2; exit 10; }
if ! mountpoint -q "$MP"; then
  OTHER=$(findmnt -nro TARGET --source "$DEV" 2>/dev/null | head -1)
  if [ -n "$OTHER" ] && [ "$OTHER" != "$MP" ]; then
    echo "mounted elsewhere at $OTHER, unmounting that first" >&2
    umount "$OTHER" || exit 12
  fi
  mkdir -p "$MP"
  if grep -qs "[[:space:]]$MP[[:space:]]" /etc/fstab; then mount "$MP" || exit 13; else mount "$DEV" "$MP" || exit 13; fi
fi
if [ -n "$LABEL" ]; then
  L=$(lsblk -no LABEL "$DEV" 2>/dev/null | head -1)
  [ "$L" = "$LABEL" ] || { echo "label is '$L', expected '$LABEL': refusing to write" >&2; umount "$MP"; exit 11; }
fi
echo "mounted $MP ($(df -h --output=avail "$MP" | tail -1 | tr -d ' ') free)" >&2
`, d.UUID, shellQuote(d.Mountpoint), shellQuote(d.Label))
	_, err := e.HostRun(ctx, script, "", nil, log)
	if ExitCode(err) == 10 {
		return ErrDiskAbsent
	}
	return err
}

// UnmountUSB unmounts every mount of the disk and powers it down when asked;
// it says plainly whether the disk is safe to unplug.
func (e *Engine) UnmountUSB(ctx context.Context, d USB, log *Log) (safe bool, err error) {
	if d.UUID == "" {
		return false, errors.New("the disk needs a filesystem UUID")
	}
	power := "0"
	if d.PowerOff {
		power = "1"
	}
	script := fmt.Sprintf(`set -u
DEV=/dev/disk/by-uuid/%[1]s
[ -e "$DEV" ] || { echo "not connected: nothing to unmount" >&2; exit 0; }
sync
for T in $(findmnt -nro TARGET --source "$DEV" 2>/dev/null); do umount "$T" || { echo "could not unmount $T (in use?)" >&2; exit 20; }; done
[ -z "$(findmnt -nro TARGET --source "$DEV" 2>/dev/null)" ] || { echo "still mounted" >&2; exit 20; }
if [ %[2]s = 1 ]; then
  P=$(lsblk -no PKNAME "$(readlink -f "$DEV")" 2>/dev/null | head -1)
  if [ -n "$P" ]; then
    udisksctl power-off -b "/dev/$P" >/dev/null 2>&1 || { [ -w "/sys/block/$P/device/delete" ] && echo 1 > "/sys/block/$P/device/delete"; } || true
    echo "unmounted and powered down: safe to unplug" >&2; exit 0
  fi
fi
echo "unmounted: safe to unplug" >&2
`, d.UUID, power)
	_, err = e.HostRun(ctx, script, "", nil, log)
	return err == nil, err
}

// DiskSpace returns free bytes and used percent of the filesystem at path.
func DiskSpace(path string) (free int64, usedPct int, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	total := int64(st.Blocks) * int64(st.Bsize)
	avail := int64(st.Bavail) * int64(st.Bsize)
	freeAll := int64(st.Bfree) * int64(st.Bsize)
	if total == 0 {
		return avail, 0, nil
	}
	used := total - freeAll
	return avail, int(float64(used) * 100 / float64(used+avail)), nil
}
