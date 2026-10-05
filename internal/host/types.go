package host

import (
	"context"

	"github.com/sarv-projects/litespm/internal/domain"
)

// HostDescriptor encapsulates metadata about a supported agent host.
type HostDescriptor struct {
	HostID                 string   `json:"hostId"`
	DisplayName            string   `json:"displayName"`
	SupportedVersions      []string `json:"supportedVersions"`
	DefaultConfigFileName  string   `json:"defaultConfigFileName"`
	ConfigFormat           string   `json:"configFormat"` // json | toml
	SupportsFormElicit     bool     `json:"supportsFormElicit"`
	RequiresBootstrapSkill bool     `json:"requiresBootstrapSkill"`
	SlashCommandTrigger    string   `json:"slashCommandTrigger"`
}

// PreExistingComponent represents a native capability detected in host configuration (read-only).
type PreExistingComponent struct {
	Name        string   `json:"name"`
	Kind        string   `json:"kind"` // mcp | skill
	Command     string   `json:"command,omitempty"`
	Args        []string `json:"args,omitempty"`
	ReadOnly    bool     `json:"readOnly"` // Always true by default
	SourcePath  string   `json:"sourcePath"`
	Description string   `json:"description,omitempty"`
}

// HostChangePlan describes proposed changes to host configuration.
type HostChangePlan struct {
	HostID          string `json:"hostId"`
	ConfigPath      string `json:"configPath"`
	OriginalContent string `json:"originalContent"`
	ProposedContent string `json:"proposedContent"`
	BackupPath      string `json:"backupPath"`
}

// HostApplyResult captures the result of applying configuration changes.
type HostApplyResult struct {
	HostID     string `json:"hostId"`
	ConfigPath string `json:"configPath"`
	BackupPath string `json:"backupPath"`
	Success    bool   `json:"success"`
}

// HostVerification describes whether LiteSPM is properly registered with the host.
type HostVerification struct {
	HostID     string `json:"hostId"`
	ConfigPath string `json:"configPath"`
	Registered bool   `json:"registered"`
	Status     string `json:"status"` // ready | missing | corrupted
}

// HostAdapter defines the contract for interacting with an agent host environment.
type HostAdapter interface {
	Descriptor() HostDescriptor
	DetectConfig(ctx context.Context, scope domain.InstallScope) (string, error)
	PlanSetup(ctx context.Context, binaryPath string, backupDir string) (*HostChangePlan, error)
	ApplySetup(ctx context.Context, plan *HostChangePlan) (*HostApplyResult, error)
	VerifySetup(ctx context.Context) (*HostVerification, error)
	DetectPreExistingComponents(ctx context.Context) ([]PreExistingComponent, error)
	RenderManualSetup(binaryPath string) string
}
