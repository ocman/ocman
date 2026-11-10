package configfile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
)

var ErrConflict = errors.New("configuration changed during save; retry the edit")

// File serializes application edits and rereads the file on every operation.
// External editors should use atomic replacement; changes observed during a save
// abort the save. A lock serializes cooperating ocman processes.
type File struct {
	Path string
	mu   sync.Mutex
}

func (f *File) Read() (*Document, error) {
	data, err := readFile(f.Path)
	if err != nil {
		return nil, err
	}
	return Parse(data)
}

func readFile(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("configuration must be a regular file")
	}
	return io.ReadAll(io.LimitReader(f, maxSize+1))
}

// Create publishes a complete migrated document without replacing an existing file.
func (f *File) Create(d *Document) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, err := d.Bytes()
	if err != nil {
		return err
	}
	return f.publish(data, nil, true)
}

func (f *File) Update(change func(*Document) error) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	lock, err := os.OpenFile(f.Path+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrConflict
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()
	before, err := readFile(f.Path)
	if err != nil {
		return err
	}
	d, err := Parse(before)
	if err != nil {
		return err
	}
	if err := change(d); err != nil {
		return err
	}
	data, err := d.Bytes()
	if err != nil {
		return err
	}
	return f.publish(data, before, false)
}

func (f *File) publish(data, before []byte, create bool) error {
	if len(data) > maxSize {
		return errors.New("configuration exceeds size limit")
	}
	dir := filepath.Dir(f.Path)
	if create {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
	} else {
		info, err := os.Lstat(f.Path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0222 == 0 {
			return errors.New("configuration is read-only or a symbolic link")
		}
	}
	tmp, err := os.CreateTemp(dir, ".ocman-config-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return err
	}
	if err := tmp.Sync(); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if create {
		// Linking is atomic and refuses to clobber a concurrently created config.
		if err := os.Link(tmp.Name(), f.Path); err != nil {
			return err
		}
	} else {
		current, err := readFile(f.Path)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, before) {
			return ErrConflict
		}
		if err := os.Rename(tmp.Name(), f.Path); err != nil {
			return err
		}
	}
	directory, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
