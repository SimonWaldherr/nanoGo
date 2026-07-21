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

	// children is a parent-path -> child-name index kept in sync with every
	// nodes mutation, so ReadDir/Remove/RemoveAll cost O(children) instead
	// of scanning every node in the whole VFS regardless of the target
	// directory's size — the flat map alone has no parent->children
	// relationship. Every method that adds or removes a node must call
	// addChildLocked/removeChildLocked (or removeSubtreeLocked for a whole
	// subtree) while holding mu, so the two stay consistent.
	children map[string]map[string]struct{}

	env map[string]string
	cwd string
}

type vfsNode struct {
	name     string
	content  []byte
	isDir    bool
	readOnly bool
	modTime  time.Time
	mode     int
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
		nodes:    map[string]*vfsNode{},
		children: map[string]map[string]struct{}{},
		env:      map[string]string{"HOME": "/home/user", "PATH": "/usr/bin:/bin", "USER": "user"},
		cwd:      "/home/user",
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
		if dir != "/" {
			fs.addChildLocked(dir)
		}
	}
	return fs
}

// addChildLocked records that childPath is a child of its parent directory in
// the children index. Caller must hold fs.mu for writing. childPath must not
// be "/" (the root has no parent to register into).
func (fs *VFS) addChildLocked(childPath string) {
	parent := path.Dir(childPath)
	if fs.children[parent] == nil {
		fs.children[parent] = map[string]struct{}{}
	}
	fs.children[parent][path.Base(childPath)] = struct{}{}
}

// removeChildLocked removes childPath from its parent's children index.
// Caller must hold fs.mu for writing.
func (fs *VFS) removeChildLocked(childPath string) {
	parent := path.Dir(childPath)
	if m, ok := fs.children[parent]; ok {
		delete(m, path.Base(childPath))
		if len(m) == 0 {
			delete(fs.children, parent)
		}
	}
}

// removeSubtreeLocked deletes abs, and — if it is a directory — every
// descendant reachable through the children index, from both fs.nodes and
// fs.children. It does not touch abs's entry in its own parent's children
// set; callers that remove a subtree's root also call removeChildLocked for
// that top-level path. Caller must hold fs.mu for writing.
func (fs *VFS) removeSubtreeLocked(abs string) {
	for name := range fs.children[abs] {
		fs.removeSubtreeLocked(path.Join(abs, name))
	}
	delete(fs.children, abs)
	delete(fs.nodes, abs)
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
	if fs.readOnlyPathLocked(abs) {
		return fmt.Errorf("write %s: read-only filesystem", p)
	}
	parent := path.Dir(abs)
	parentNode, ok := fs.nodes[parent]
	if !ok || !parentNode.isDir {
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
	fs.addChildLocked(abs)
	return nil
}

// Mkdir creates a single directory. The parent directory must exist.
func (fs *VFS) Mkdir(p string, mode int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	if fs.readOnlyPathLocked(abs) {
		return fmt.Errorf("mkdir %s: read-only filesystem", p)
	}
	parent := path.Dir(abs)
	parentNode, ok := fs.nodes[parent]
	if !ok || !parentNode.isDir {
		return fmt.Errorf("mkdir %s: no such file or directory", p)
	}
	if _, ok := fs.nodes[abs]; ok {
		return fmt.Errorf("mkdir %s: file exists", p)
	}
	if mode == 0 {
		mode = 0755
	}
	fs.nodes[abs] = &vfsNode{name: path.Base(abs), isDir: true, modTime: time.Now(), mode: mode}
	fs.addChildLocked(abs)
	return nil
}

// MkdirAll creates the named directory and all missing parent directories.
func (fs *VFS) MkdirAll(p string, mode int) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	if fs.readOnlyPathLocked(abs) {
		return fmt.Errorf("mkdir %s: read-only filesystem", p)
	}
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
			fs.addChildLocked(current)
		}
	}
	return nil
}

// Remove removes a file or empty directory.
func (fs *VFS) Remove(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	if fs.readOnlyPathLocked(abs) {
		return fmt.Errorf("remove %s: read-only filesystem", p)
	}
	node, ok := fs.nodes[abs]
	if !ok {
		return fmt.Errorf("remove %s: no such file or directory", p)
	}
	if node.isDir && len(fs.children[abs]) > 0 {
		return fmt.Errorf("remove %s: directory not empty", p)
	}
	delete(fs.nodes, abs)
	fs.removeChildLocked(abs)
	return nil
}

// RemoveAll removes the named path and any children it contains.
func (fs *VFS) RemoveAll(p string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	abs := cleanPath(p, fs.cwd)
	if fs.readOnlyPathLocked(abs) {
		return fmt.Errorf("remove %s: read-only filesystem", p)
	}
	if _, ok := fs.nodes[abs]; !ok {
		return nil // not an error per os.RemoveAll contract
	}
	fs.removeSubtreeLocked(abs)
	fs.removeChildLocked(abs)
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
	for name := range fs.children[abs] {
		node, ok := fs.nodes[path.Join(abs, name)]
		if !ok {
			continue // children index and nodes must stay in sync; defensive only
		}
		infos = append(infos, &VFSFileInfo{
			Name:    node.name,
			Size:    len(node.content),
			IsDir:   node.isDir,
			ModTime: node.modTime,
			Mode:    node.mode,
		})
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

// readOnlyPathLocked reports whether abs is within a read-only mounted tree.
// The caller must hold fs.mu for reading or writing.
func (fs *VFS) readOnlyPathLocked(abs string) bool {
	for current := abs; ; current = path.Dir(current) {
		if node, ok := fs.nodes[current]; ok && node.readOnly {
			return true
		}
		if current == "/" {
			return false
		}
	}
}
