package interp

import (
	"context"
	"errors"
	"fmt"
	"io"
	iofs "io/fs"
	"os"
	"path"
	"strings"
	"time"
)

// DefaultVFSImportMaxFiles and DefaultVFSImportMaxBytes bound host imports by
// default. They avoid turning an accidentally broad directory or an unbounded
// reader into a large in-memory allocation.
const (
	DefaultVFSImportMaxFiles       = 10_000
	DefaultVFSImportMaxBytes int64 = 16 << 20
)

// VFSImportOptions controls a host-side snapshot import. A zero MaxFiles or
// MaxBytes uses the safe defaults above. ReadOnly seals the imported tree from
// guest VFS mutations; capability checks still apply independently.
type VFSImportOptions struct {
	ReadOnly bool
	MaxFiles int
	MaxBytes int64
}

type vfsImportEntry struct {
	relative string
	isDir    bool
	content  []byte
	mode     int
	modTime  time.Time
}

// MountFS snapshots an io/fs filesystem into a read-only VFS tree. It works
// directly with embed.FS and fs.Sub(embedFS, "assets"). The source is copied;
// guest code never receives access to the host filesystem implementation.
func (vfs *VFS) MountFS(prefix string, source iofs.FS) error {
	return vfs.ImportFS(prefix, source, VFSImportOptions{ReadOnly: true})
}

// ImportDir snapshots a host directory into the VFS. It does not create a
// live host-directory mount, deliberately avoiding future host filesystem
// access from guest code. Use ReadOnly for untrusted guest code unless writes
// into the imported copy are explicitly intended.
func (vfs *VFS) ImportDir(prefix, hostDir string, options VFSImportOptions) error {
	if hostDir == "" {
		return errors.New("nanogo: host directory must not be empty")
	}
	return vfs.ImportFS(prefix, os.DirFS(hostDir), options)
}

// ImportFS snapshots source under prefix. Only regular files and directories
// are imported; symlinks and special files are rejected rather than followed
// or represented ambiguously in the VFS.
func (vfs *VFS) ImportFS(prefix string, source iofs.FS, options VFSImportOptions) error {
	if vfs == nil {
		return errors.New("nanogo: nil VFS")
	}
	if source == nil {
		return errors.New("nanogo: nil source filesystem")
	}
	maxFiles, maxBytes, err := importLimits(options)
	if err != nil {
		return err
	}
	entries, err := snapshotFS(source, maxFiles, maxBytes)
	if err != nil {
		return err
	}
	return vfs.applyImport(prefix, entries, options.ReadOnly)
}

// ImportReader copies at most maxBytes from reader into one writable VFS file.
// A zero limit uses DefaultVFSImportMaxBytes. Cancellation is checked between
// Read calls; a reader that blocks inside Read must still be interrupted by its
// host implementation.
func (vfs *VFS) ImportReader(ctx context.Context, filePath string, reader io.Reader, maxBytes int64) error {
	if vfs == nil {
		return errors.New("nanogo: nil VFS")
	}
	if reader == nil {
		return errors.New("nanogo: nil reader")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if maxBytes == 0 {
		maxBytes = DefaultVFSImportMaxBytes
	}
	if maxBytes < 0 {
		return errors.New("nanogo: reader import limit must not be negative")
	}
	data, err := readWithContext(ctx, reader, maxBytes)
	if err != nil {
		return err
	}
	abs := vfs.ResolvePath(filePath)
	if err := vfs.MkdirAll(path.Dir(abs), 0755); err != nil {
		return err
	}
	return vfs.WriteFile(abs, data, 0644)
}

func importLimits(options VFSImportOptions) (int, int64, error) {
	maxFiles, maxBytes := options.MaxFiles, options.MaxBytes
	if maxFiles == 0 {
		maxFiles = DefaultVFSImportMaxFiles
	}
	if maxBytes == 0 {
		maxBytes = DefaultVFSImportMaxBytes
	}
	if maxFiles < 0 || maxBytes < 0 {
		return 0, 0, errors.New("nanogo: VFS import limits must not be negative")
	}
	return maxFiles, maxBytes, nil
}

func snapshotFS(source iofs.FS, maxFiles int, maxBytes int64) ([]vfsImportEntry, error) {
	entries := make([]vfsImportEntry, 0)
	fileCount := 0
	var totalBytes int64
	err := iofs.WalkDir(source, ".", func(relative string, entry iofs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if relative == "." {
			return nil
		}
		if !iofs.ValidPath(relative) {
			return fmt.Errorf("nanogo: invalid source path %q", relative)
		}
		if entry.Type()&iofs.ModeSymlink != 0 {
			return fmt.Errorf("nanogo: refusing symlink %q in VFS import", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.IsDir() {
			entries = append(entries, vfsImportEntry{relative: relative, isDir: true, mode: int(info.Mode().Perm()), modTime: info.ModTime()})
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("nanogo: refusing non-regular file %q in VFS import", relative)
		}
		fileCount++
		if fileCount > maxFiles {
			return fmt.Errorf("nanogo: VFS import exceeds %d files", maxFiles)
		}
		remaining := maxBytes - totalBytes
		if remaining < 0 {
			return fmt.Errorf("nanogo: VFS import exceeds %d bytes", maxBytes)
		}
		file, err := source.Open(relative)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, remaining+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if int64(len(data)) > remaining {
			return fmt.Errorf("nanogo: VFS import exceeds %d bytes", maxBytes)
		}
		totalBytes += int64(len(data))
		entries = append(entries, vfsImportEntry{relative: relative, content: data, mode: int(info.Mode().Perm()), modTime: info.ModTime()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (vfs *VFS) applyImport(prefix string, entries []vfsImportEntry, readOnly bool) error {
	vfs.mu.Lock()
	defer vfs.mu.Unlock()
	root := cleanPath(prefix, vfs.cwd)
	if root == "/" {
		return errors.New("nanogo: cannot import over VFS root")
	}
	if vfs.readOnlyPathLocked(root) {
		return fmt.Errorf("nanogo: import target %s is read-only", root)
	}
	if err := vfs.ensureDirectoryLocked(path.Dir(root), false); err != nil {
		return err
	}
	if err := vfs.ensureDirectoryLocked(root, readOnly); err != nil {
		return err
	}
	for _, entry := range entries {
		target := path.Join(root, entry.relative)
		if entry.isDir {
			if err := vfs.ensureDirectoryLocked(target, readOnly); err != nil {
				return err
			}
			continue
		}
		if err := vfs.ensureDirectoryLocked(path.Dir(target), readOnly); err != nil {
			return err
		}
		if existing, ok := vfs.nodes[target]; ok && existing.isDir {
			return fmt.Errorf("nanogo: import target %s is a directory", target)
		}
		content := make([]byte, len(entry.content))
		copy(content, entry.content)
		mode := entry.mode
		if mode == 0 {
			mode = 0644
		}
		vfs.nodes[target] = &vfsNode{name: path.Base(target), content: content, modTime: entry.modTime, mode: mode, readOnly: readOnly}
		vfs.addChildLocked(target)
	}
	vfs.revision++
	return nil
}

func (vfs *VFS) ensureDirectoryLocked(target string, readOnly bool) error {
	parts := strings.Split(strings.TrimPrefix(target, "/"), "/")
	current := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		if existing, ok := vfs.nodes[current]; ok {
			if !existing.isDir {
				return fmt.Errorf("nanogo: import target %s is not a directory", current)
			}
			if current == target && readOnly {
				existing.readOnly = true
			}
			continue
		}
		vfs.nodes[current] = &vfsNode{name: path.Base(current), isDir: true, readOnly: readOnly, modTime: time.Now(), mode: 0755}
		vfs.addChildLocked(current)
	}
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	select {
	case <-r.ctx.Done():
		return 0, r.ctx.Err()
	default:
		return r.r.Read(p)
	}
}

func readWithContext(ctx context.Context, reader io.Reader, maxBytes int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx: ctx, r: reader}, maxBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("nanogo: reader import exceeds %d bytes", maxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}
