package interp

// Clone takes one consistent snapshot of file data, environment, cwd and
// read-only flags. The returned filesystem shares no mutable storage with fs.
func (fs *VFS) Clone() *VFS {
	if fs == nil {
		return NewVFS()
	}
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	out := &VFS{nodes: make(map[string]*vfsNode, len(fs.nodes)), children: map[string]map[string]struct{}{}, env: make(map[string]string, len(fs.env)), cwd: fs.cwd, revision: fs.revision}
	for name, node := range fs.nodes {
		copyNode := *node
		copyNode.content = append([]byte(nil), node.content...)
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
