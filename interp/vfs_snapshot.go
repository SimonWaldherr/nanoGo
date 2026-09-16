package interp

// Clone takes one consistent snapshot of file data, environment, cwd and
// read-only flags. The returned filesystem shares no mutable storage with fs.
// File contents are shared internally until overwritten; metadata and indexes
// are copied eagerly. Reads and public writes retain their defensive copies.
func (fs *VFS) Clone() *VFS {
	if fs == nil {
		return NewVFS()
	}
	// Mark both nodes while holding the original filesystem's write lock:
	// subsequent writes to either filesystem must detach the shared bytes.
	fs.mu.Lock()
	defer fs.mu.Unlock()
	out := &VFS{nodes: make(map[string]*vfsNode, len(fs.nodes)), children: map[string]map[string]struct{}{}, env: make(map[string]string, len(fs.env)), cwd: fs.cwd, revision: fs.revision}
	for name, node := range fs.nodes {
		node.sharedContent = true
		copyNode := *node
		out.nodes[name] = &copyNode
		if name != "/" {
			out.addChildLocked(name)
		}
	}
	for k, v := range fs.env {
		out.env[k] = v
	}
	return out
}
