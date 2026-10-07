// Package runtime adapts resolved packages to executable launches (ARCH/17
// §5): Node/npm (npx), Python/uv (uvx), native binaries, OCI containers and
// remote endpoints. Adapters plan launches; they never execute package code
// at plan time. Availability probing uses PATH lookup only.
package runtime

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Kind identifies the runtime family.
type Kind string

const (
	KindNode   Kind = "node"
	KindPython Kind = "python"
	KindNative Kind = "native"
	KindOCI    Kind = "oci"
	KindRemote Kind = "remote"
)

// Spec is a materialisation request derived from a version record.
type Spec struct {
	Kind       Kind
	Command    string
	Args       []string
	Env        map[string]string
	Endpoint   string
	Image      string
	WorkingDir string
}

// Launch is a planned, executable launch line.
type Launch struct {
	Executable string            `json:"executable"`
	Args       []string          `json:"args"`
	Env        map[string]string `json:"env,omitempty"`
	Transport  string            `json:"transport"`
	Notes      []string          `json:"notes,omitempty"`
}

// Adapter plans launches for one runtime family.
type Adapter interface {
	Kind() Kind
	// Available reports whether the toolchain exists (PATH lookup only).
	Available(ctx context.Context) bool
	// Plan turns a spec into an executable launch or a fail-closed error.
	Plan(ctx context.Context, spec Spec) (Launch, error)
}

// Registry holds the adapters.
type Registry struct {
	adapters map[Kind]Adapter
}

// NewRegistry returns the default adapter set.
func NewRegistry() *Registry {
	r := &Registry{adapters: map[Kind]Adapter{}}
	r.Register(NodeAdapter{})
	r.Register(PythonAdapter{})
	r.Register(NativeAdapter{})
	r.Register(OCIAdapter{})
	r.Register(RemoteAdapter{})
	return r
}

// Register adds or replaces an adapter.
func (r *Registry) Register(a Adapter) { r.adapters[a.Kind()] = a }

// Get returns the adapter for kind.
func (r *Registry) Get(k Kind) (Adapter, bool) { a, ok := r.adapters[k]; return a, ok }

// Kinds returns registered kinds in stable order.
func (r *Registry) Kinds() []Kind {
	out := []Kind{KindNode, KindPython, KindNative, KindOCI, KindRemote}
	var kept []Kind
	for _, k := range out {
		if _, ok := r.adapters[k]; ok {
			kept = append(kept, k)
		}
	}
	return kept
}

func lookPath(names ...string) string {
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// NodeAdapter plans npx/npm launches.
type NodeAdapter struct{}

func (NodeAdapter) Kind() Kind { return KindNode }
func (NodeAdapter) Available(_ context.Context) bool {
	return lookPath("npx", "npm", "node") != ""
}
func (NodeAdapter) Plan(_ context.Context, spec Spec) (Launch, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return Launch{}, fmt.Errorf("LPSM-RUNTIME-NODE: command is required")
	}
	exe := spec.Command
	if exe == "npx" || exe == "npm" || exe == "node" {
		if p := lookPath(exe); p != "" {
			exe = p
		}
	}
	return Launch{Executable: exe, Args: spec.Args, Env: spec.Env, Transport: "stdio"}, nil
}

// PythonAdapter plans uvx/pipx launches.
type PythonAdapter struct{}

func (PythonAdapter) Kind() Kind { return KindPython }
func (PythonAdapter) Available(_ context.Context) bool {
	return lookPath("uvx", "uv", "pipx", "python3") != ""
}
func (PythonAdapter) Plan(_ context.Context, spec Spec) (Launch, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return Launch{}, fmt.Errorf("LPSM-RUNTIME-PYTHON: command is required")
	}
	exe := spec.Command
	if exe == "uvx" || exe == "uv" || exe == "pipx" || exe == "python3" {
		if p := lookPath(exe); p != "" {
			exe = p
		}
	}
	return Launch{Executable: exe, Args: spec.Args, Env: spec.Env, Transport: "stdio"}, nil
}

// NativeAdapter plans direct binary launches. The binary must be an absolute
// path or resolve on PATH; bare command names that do not resolve fail
// closed instead of executing from the working directory.
type NativeAdapter struct{}

func (NativeAdapter) Kind() Kind { return KindNative }
func (NativeAdapter) Available(_ context.Context) bool { return true }
func (NativeAdapter) Plan(_ context.Context, spec Spec) (Launch, error) {
	if strings.TrimSpace(spec.Command) == "" {
		return Launch{}, fmt.Errorf("LPSM-RUNTIME-NATIVE: command is required")
	}
	exe := spec.Command
	if !strings.Contains(exe, "/") && !strings.Contains(exe, "\\") {
		p, err := exec.LookPath(exe)
		if err != nil {
			return Launch{}, fmt.Errorf("LPSM-RUNTIME-NATIVE-NOT-FOUND: %q is not on PATH", exe)
		}
		exe = p
	}
	return Launch{Executable: exe, Args: spec.Args, Env: spec.Env, Transport: "stdio"}, nil
}

// OCIAdapter plans container launches. It requires an image reference and
// records the digest pin requirement; tag-only references are refused unless
// explicitly allowed by the caller (allowUnpinned).
type OCIAdapter struct{}

func (OCIAdapter) Kind() Kind { return KindOCI }
func (OCIAdapter) Available(_ context.Context) bool {
	return lookPath("docker") != ""
}
func (OCIAdapter) Plan(_ context.Context, spec Spec) (Launch, error) {
	return PlanOCI(spec, false)
}

// PlanOCI builds a docker run line. allowUnpinned permits tag-only
// references (used only for discovery previews, never for installs).
func PlanOCI(spec Spec, allowUnpinned bool) (Launch, error) {
	if strings.TrimSpace(spec.Image) == "" {
		return Launch{}, fmt.Errorf("LPSM-RUNTIME-OCI: image is required")
	}
	if !strings.Contains(spec.Image, "@sha256:") && !allowUnpinned {
		return Launch{}, fmt.Errorf("LPSM-RUNTIME-OCI-UNPINNED: %q is not digest-pinned", spec.Image)
	}
	docker := lookPath("docker")
	if docker == "" {
		docker = "docker"
	}
	args := []string{"run", "--rm", "-i"}
	for k, v := range spec.Env {
		args = append(args, "-e", k+"="+v)
	}
	args = append(args, spec.Image)
	args = append(args, spec.Args...)
	return Launch{
		Executable: docker,
		Args:       args,
		Transport:  "stdio",
		Notes:      []string{"oci image: " + spec.Image},
	}, nil
}

// RemoteAdapter plans remote-endpoint bindings. No process is launched; the
// endpoint origin allow-list check happens at invoke time.
type RemoteAdapter struct{}

func (RemoteAdapter) Kind() Kind { return KindRemote }
func (RemoteAdapter) Available(_ context.Context) bool { return true }
func (RemoteAdapter) Plan(_ context.Context, spec Spec) (Launch, error) {
	if strings.TrimSpace(spec.Endpoint) == "" {
		return Launch{}, fmt.Errorf("LPSM-RUNTIME-REMOTE: endpoint is required")
	}
	if !strings.HasPrefix(spec.Endpoint, "https://") && !strings.HasPrefix(spec.Endpoint, "http://localhost") {
		return Launch{}, fmt.Errorf("LPSM-RUNTIME-REMOTE-SCHEME: refusing non-https endpoint %q", spec.Endpoint)
	}
	return Launch{Executable: "", Args: nil, Env: spec.Env, Transport: "streamable-http",
		Notes: []string{"remote endpoint: " + spec.Endpoint}}, nil
}
