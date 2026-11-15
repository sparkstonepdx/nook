package core

// Reading an app's folder without following links: every step opens the next directory relative to the last with
// O_NOFOLLOW, so an app can't swap a folder for a link to somewhere it shouldn't reach while nook reads it.

import (
	"os"
	"sort"
	"syscall"
)

// dirFD opens a directory without following a symlink at its last component.
type dirFD struct{ fd int }

func openDirNoFollow(path string) (*dirFD, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return &dirFD{fd}, nil
}

func (d *dirFD) close() { syscall.Close(d.fd) }

const oPath = 0x200000 // O_PATH (same value on amd64 and arm64)

// fstatat(fd, name, AT_SYMLINK_NOFOLLOW) via openat(O_PATH|O_NOFOLLOW) + fstat: describes a symlink itself, never its target.
func lstatAt(dirfd int, name string) (*syscall.Stat_t, error) {
	fd, err := syscall.Openat(dirfd, name, oPath|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	var st syscall.Stat_t
	if err := syscall.Fstat(fd, &st); err != nil {
		return nil, err
	}
	return &st, nil
}

func (d *dirFD) lstat(name string) (*syscall.Stat_t, error) { return lstatAt(d.fd, name) }

// lstatIn checks name/child relative to d without following either component.
func (d *dirFD) lstatIn(name, child string) (*syscall.Stat_t, error) {
	fd, err := syscall.Openat(d.fd, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer syscall.Close(fd)
	return lstatAt(fd, child)
}

func (d *dirFD) names() []string {
	f := os.NewFile(uintptr(dupFD(d.fd)), "dir")
	defer f.Close()
	list, _ := f.Readdirnames(-1)
	sort.Strings(list)
	return list
}

func dupFD(fd int) int { n, _ := syscall.Dup(fd); return n }

func (d *dirFD) unlink(name string) { syscall.Unlinkat(d.fd, name) }

func isDir(st *syscall.Stat_t) bool  { return st.Mode&syscall.S_IFMT == syscall.S_IFDIR }
func isFile(st *syscall.Stat_t) bool { return st.Mode&syscall.S_IFMT == syscall.S_IFREG }
