package interp

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"strings"
)

// Capabilities is the allow-list for the built-in fs, os, and http packages.
// It is intentionally deny-by-default. Configure it before calling RunContext.
// Directly registered host natives are capabilities in their own right and
// remain the host application's responsibility.
type Capabilities struct {
	FileSystem FileSystemCapabilities
	Network    NetworkCapabilities
}

// FileSystemCapabilities applies to nanoGo's virtual filesystem and the
// host-proxied fs.ReadFile function. It never grants access to the host OS by
// itself; a host native must separately choose to expose such an operation.
type FileSystemCapabilities struct {
	Read  bool
	Write bool

	// ReadPaths and WritePaths optionally restrict operations to absolute VFS
	// paths. A configured path grants access to that path and its descendants.
	// An empty list means every VFS path is allowed for the corresponding
	// operation. Paths are matched after resolving relative paths and "..".
	ReadPaths  []string
	WritePaths []string
}

// NetworkCapabilities applies to the built-in http package. HTTP enables
// HTTP(S) requests. When AllowedHosts is non-empty, every request must match
// an exact host or a wildcard such as "*.example.com". Literal loopback,
// private, link-local, and unspecified IP addresses remain blocked unless
// AllowPrivateHosts is set.
//
// The host transport must still enforce its own DNS/IP egress policy: a
// hostname can resolve differently after this URL-level check.
type NetworkCapabilities struct {
	HTTP              bool
	AllowedHosts      []string
	AllowPrivateHosts bool
}

// FullCapabilities enables the curated VFS and HTTP interfaces without a host
// allow-list. It is intended for trusted code and tests; untrusted workloads
// should grant only the smallest required subset explicitly.
func FullCapabilities() Capabilities {
	return Capabilities{
		FileSystem: FileSystemCapabilities{Read: true, Write: true},
		Network:    NetworkCapabilities{HTTP: true, AllowPrivateHosts: true},
	}
}

func (vm *Interpreter) requireFileRead(operation, filePath string) (string, error) {
	policy := vm.Capabilities.FileSystem
	resolvedPath := vm.resolveFilePath(filePath)
	if policy.Read && filePathAllowed(resolvedPath, policy.ReadPaths) {
		return resolvedPath, nil
	}
	vm.emitTrace("capability_denied", operation, "filesystem read "+filePath, nil)
	return "", NewRuntimeError("filesystem read denied: " + operation)
}

func (vm *Interpreter) requireFileWrite(operation, filePath string) (string, error) {
	policy := vm.Capabilities.FileSystem
	resolvedPath := vm.resolveFilePath(filePath)
	if policy.Write && filePathAllowed(resolvedPath, policy.WritePaths) {
		return resolvedPath, nil
	}
	vm.emitTrace("capability_denied", operation, "filesystem write "+filePath, nil)
	return "", NewRuntimeError("filesystem write denied: " + operation)
}

func (vm *Interpreter) resolveFilePath(filePath string) string {
	// Environment access is governed by Read/Write but is not a filesystem path.
	if filePath == "" {
		return ""
	}
	return vm.VFS.ResolvePath(filePath)
}

func filePathAllowed(resolvedPath string, allowedPaths []string) bool {
	if resolvedPath == "" || len(allowedPaths) == 0 {
		return true
	}
	for _, allowed := range allowedPaths {
		if !path.IsAbs(allowed) {
			continue
		}
		root := path.Clean(allowed)
		if root == "/" || resolvedPath == root || strings.HasPrefix(resolvedPath, root+"/") {
			return true
		}
	}
	return false
}

func (vm *Interpreter) requireHTTP(rawURL string) error {
	policy := vm.Capabilities.Network
	if !policy.HTTP {
		vm.emitTrace("capability_denied", "http", "network", nil)
		return NewRuntimeError("network access denied: http")
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return NewRuntimeError("network access denied: invalid HTTP(S) URL")
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if !hostAllowed(host, policy.AllowedHosts) {
		vm.emitTrace("capability_denied", "http", "host "+host, nil)
		return NewRuntimeError("network access denied: host " + host)
	}
	if ip := net.ParseIP(host); ip != nil && !policy.AllowPrivateHosts && isPrivateIP(ip) {
		vm.emitTrace("capability_denied", "http", "private host "+host, nil)
		return NewRuntimeError("network access denied: private host " + host)
	}
	return nil
}

func hostAllowed(host string, allowedHosts []string) bool {
	if len(allowedHosts) == 0 {
		return true
	}
	for _, allowed := range allowedHosts {
		allowed = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(allowed), "."))
		if allowed == host {
			return true
		}
		if strings.HasPrefix(allowed, "*.") {
			suffix := strings.TrimPrefix(allowed, "*")
			if strings.HasSuffix(host, suffix) && host != strings.TrimPrefix(suffix, ".") {
				return true
			}
		}
	}
	return false
}

func isPrivateIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// CapabilitySummary returns a compact, safe-to-log description of the active
// policy. It intentionally reports no host-native registrations or secrets.
func (c Capabilities) CapabilitySummary() string {
	hosts := "any"
	if len(c.Network.AllowedHosts) > 0 {
		hosts = strings.Join(c.Network.AllowedHosts, ",")
	}
	return fmt.Sprintf("fs(read=%t,write=%t) http=%t hosts=%s", c.FileSystem.Read, c.FileSystem.Write, c.Network.HTTP, hosts)
}
