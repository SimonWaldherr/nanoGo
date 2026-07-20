// interp/vfs.go
package interp

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// VFS is a thread-safe in-memory virtual filesystem with Unix-like path semantics.
// It is pre-populated with a minimal set of standard directories and can be shared
// across multiple Interpreter instances (e.g. across tool calls in an MCP session).
type VFS struct {
	mu    sync.RWMutex
	nodes map[string]*vfsNode
	env   map[string]string
	cwd   string
}

type vfsNode struct {
	name    string
	content []byte
	isDir   bool
	modTime time.Time
	mode    int
}

// VFSFileInfo describes a file or directory entry returned by Stat / ReadDir.
type VFSFileInfo struct {
	Name    string
	Size    int
	IsDir   bool
	ModTime time.Time
	Mode    int
}

// NewVFS creates a VFS pre-populated with standard Unix directories.
func NewVFS() *VFS {
	now := time.Now()
	fs := &VFS{
		nodes: map[string]*vfsNode{},
		env:   map[string]string{"HOME": "/home/user", "PATH": "/usr/bin:/bin", "USER": "user"},
		cwd:   "/home/user",
	}
	for _, dir := range []string{
		"/", "/bin", "/etc", "/home", "/home/user",
		"/tmp", "/usr", "/usr/bin", "/var", "/var/log",
	} {
		fs.nodes[dir] = &vfsNode{
			name:    path.Base(dir),
			isDir:   true,
			modTime: now,
			mode:    0755,
		}
	}
	return fs
}

// cleanPath resolves a path to an absolute, cleaned path.
// Relative paths are resolved against cwd.
// The caller is responsible for reading fs.cwd under the appropriate lock.
func cleanPath(p, cwd string) string {
	if !path.IsAbs(p) {
		p = cwd + "/" + p
	}
	return path.Clean(p)
}

// Getwd returns the current working directory.
func (fs *VFS) Getwd() string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.cwd
}

// ResolvePath returns p as a clean, absolute VFS path using the current
// working directory. It performs no I/O and is useful for capability checks.
func (fs *VFS) ResolvePath(p string) string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return cleanPath(p, fs.cwd)
}

// Chdir changes the current working directory.
func (fs *VFS) Chdir(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	node, ok := fs.nodes[abs]
	if !ok {
		return fmt.Errorf("chdir %s: no such file or directory", p)
	}
	if !node.isDir {
		return fmt.Errorf("chdir %s: not a directory", p)
	}
	fs.cwd = abs
	return nil
}

// ReadFile returns the contents of the named file.
func (fs *VFS) ReadFile(p string) ([]byte, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	abs := cleanPath(p, fs.cwd)
	node, ok := fs.nodes[abs]
	if !ok {
		return nil, fmt.Errorf("open %s: no such file or directory", p)
	}
	if node.isDir {
		return nil, fmt.Errorf("read %s: is a directory", p)
	}
	out := make([]byte, len(node.content))
	copy(out, node.content)
	return out, nil
}

// WriteFile creates or overwrites the named file with the given data.
// The parent directory must already exist.
func (fs *VFS) WriteFile(p string, data []byte, mode int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	parent := path.Dir(abs)
	if _, ok := fs.nodes[parent]; !ok {
		return fmt.Errorf("open %s: no such file or directory", p)
	}
	content := make([]byte, len(data))
	copy(content, data)
	if mode == 0 {
		mode = 0644
	}
	fs.nodes[abs] = &vfsNode{
		name:    path.Base(abs),
		content: content,
		isDir:   false,
		modTime: time.Now(),
		mode:    mode,
	}
	return nil
}

// Mkdir creates a single directory. The parent directory must exist.
func (fs *VFS) Mkdir(p string, mode int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	parent := path.Dir(abs)
	if _, ok := fs.nodes[parent]; !ok {
		return fmt.Errorf("mkdir %s: no such file or directory", p)
	}
	if _, ok := fs.nodes[abs]; ok {
		return fmt.Errorf("mkdir %s: file exists", p)
	}
	if mode == 0 {
		mode = 0755
	}
	fs.nodes[abs] = &vfsNode{name: path.Base(abs), isDir: true, modTime: time.Now(), mode: mode}
	return nil
}

// MkdirAll creates the named directory and all missing parent directories.
func (fs *VFS) MkdirAll(p string, mode int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	if mode == 0 {
		mode = 0755
	}
	parts := strings.Split(strings.TrimPrefix(abs, "/"), "/")
	current := "/"
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		if _, ok := fs.nodes[current]; !ok {
			fs.nodes[current] = &vfsNode{name: part, isDir: true, modTime: time.Now(), mode: mode}
		}
	}
	return nil
}

// Remove removes a file or empty directory.
func (fs *VFS) Remove(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	node, ok := fs.nodes[abs]
	if !ok {
		return fmt.Errorf("remove %s: no such file or directory", p)
	}
	if node.isDir {
		for existing := range fs.nodes {
			if existing != abs && path.Dir(existing) == abs {
				return fmt.Errorf("remove %s: directory not empty", p)
			}
		}
	}
	delete(fs.nodes, abs)
	return nil
}

// RemoveAll removes the named path and any children it contains.
func (fs *VFS) RemoveAll(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	if _, ok := fs.nodes[abs]; !ok {
		return nil // not an error per os.RemoveAll contract
	}
	for existing := range fs.nodes {
		if existing == abs || strings.HasPrefix(existing, abs+"/") {
			delete(fs.nodes, existing)
		}
	}
	return nil
}

// Stat returns information about the named path.
func (fs *VFS) Stat(p string) (*VFSFileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	abs := cleanPath(p, fs.cwd)
	node, ok := fs.nodes[abs]
	if !ok {
		return nil, fmt.Errorf("stat %s: no such file or directory", p)
	}
	return &VFSFileInfo{
		Name:    node.name,
		Size:    len(node.content),
		IsDir:   node.isDir,
		ModTime: node.modTime,
		Mode:    node.mode,
	}, nil
}

// ReadDir returns the entries in the named directory, sorted by name.
func (fs *VFS) ReadDir(p string) ([]*VFSFileInfo, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	abs := cleanPath(p, fs.cwd)
	if _, ok := fs.nodes[abs]; !ok {
		return nil, fmt.Errorf("open %s: no such file or directory", p)
	}
	var infos []*VFSFileInfo
	for nodePath, node := range fs.nodes {
		if nodePath != abs && path.Dir(nodePath) == abs {
			infos = append(infos, &VFSFileInfo{
				Name:    node.name,
				Size:    len(node.content),
				IsDir:   node.isDir,
				ModTime: node.modTime,
				Mode:    node.mode,
			})
		}
	}
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos, nil
}

// Getenv returns the value of the environment variable.
func (fs *VFS) Getenv(key string) string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	return fs.env[key]
}

// Setenv sets an environment variable.
func (fs *VFS) Setenv(key, val string) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	fs.env[key] = val
}

// Environ returns all environment variables as a slice of "key=value" strings.
func (fs *VFS) Environ() []string {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	out := make([]string, 0, len(fs.env))
	for k, v := range fs.env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
