package install

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sarv-projects/litepsm/internal/artifact"
	"github.com/sarv-projects/litepsm/internal/domain"
	"github.com/sarv-projects/litepsm/internal/state"
)

// ArchiveSourceFunc retrieves an archive reader and format for a listing version.
type ArchiveSourceFunc func(ctx context.Context, listingID string, version string) (io.ReadCloser, string, error)

// TreeSourceFunc provides pre-extracted directory contents into staging.
type TreeSourceFunc func(ctx context.Context, targetStagingDir string) (*domain.ExtractedTreeInfo, error)

// InstallOptions holds parameters for an installation operation.
type InstallOptions struct {
	ListingID     string
	Version       string
	Scope         domain.InstallScope
	WorkspaceID   string
	ProjectRoot   string
	ApprovalID    string
	ArchiveSource ArchiveSourceFunc
	TreeSource    TreeSourceFunc
	Limits        *artifact.ExtractionLimits
}

// Engine coordinates artifact acquisition, CAS placement, and SQLite transactional commits.
type Engine struct {
	db          *state.DB
	casRoot     string
	stagingRoot string
}

// NewEngine creates a new installation engine.
func NewEngine(db *state.DB, casRoot, stagingRoot string) (*Engine, error) {
	if err := os.MkdirAll(casRoot, 0700); err != nil {
		return nil, fmt.Errorf("failed to create CAS root %s: %w", casRoot, err)
	}
	if err := os.MkdirAll(stagingRoot, 0700); err != nil {
		return nil, fmt.Errorf("failed to create staging root %s: %w", stagingRoot, err)
	}

	return &Engine{
		db:          db,
		casRoot:     filepath.Clean(casRoot),
		stagingRoot: filepath.Clean(stagingRoot),
	}, nil
}

// Execute performs safe installation with atomic CAS promotion and safe rollback.
func (e *Engine) Execute(ctx context.Context, opts InstallOptions) (*domain.InstallRecord, error) {
	if strings.TrimSpace(opts.ListingID) == "" {
		return nil, domain.ErrInvalidIdentifier(opts.ListingID, "valid non-empty listing id")
	}
	if strings.TrimSpace(opts.Version) == "" {
		opts.Version = "latest"
	}
	if opts.Scope == "" {
		opts.Scope = domain.ScopeUser
	}

	limits := artifact.DefaultExtractionLimits
	if opts.Limits != nil {
		limits = *opts.Limits
	}

	sum := sha256.Sum256([]byte(opts.ListingID + opts.Version))
	opID := fmt.Sprintf("op_%d_%x", time.Now().UnixNano(), sum[:4])
	stagingDir := filepath.Join(e.stagingRoot, opID)

	// Step 1: Initialize journal entry
	if err := e.db.CreateOperation(ctx, opID, nil, "install", ""); err != nil {
		return nil, fmt.Errorf("failed to initialize operation %s: %w", opID, err)
	}

	// Step 2: Enforce approval consumption if required
	if opts.ApprovalID != "" {
		if err := e.db.ConsumeApproval(ctx, opts.ApprovalID); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
	}

	// Step 3: Fetch & Stage artifact
	if err := e.db.AdvanceOperationState(ctx, opID, "fetching"); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}

	if err := os.MkdirAll(stagingDir, 0700); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, fmt.Errorf("failed to create staging dir %s: %w", stagingDir, err)
	}

	extractedDir := filepath.Join(stagingDir, "extracted")

	var treeInfo *domain.ExtractedTreeInfo
	var extractErr error

	if opts.TreeSource != nil {
		if err := e.db.AdvanceOperationState(ctx, opID, "staging"); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
		treeInfo, extractErr = opts.TreeSource(ctx, extractedDir)
	} else if opts.ArchiveSource != nil {
		rc, format, err := opts.ArchiveSource(ctx, opts.ListingID, opts.Version)
		if err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed to fetch archive source: %w", err)
		}
		defer rc.Close()

		archivePath := filepath.Join(stagingDir, "source.archive")
		archiveFile, err := os.Create(archivePath)
		if err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed to create staging archive file: %w", err)
		}

		_, _, spoolErr := artifact.SpoolDownloadBounded(rc, limits.MaxArchiveSize, archiveFile)
		archiveFile.Close()
		if spoolErr != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed during bounded spooling: %w", spoolErr)
		}

		if err := e.db.AdvanceOperationState(ctx, opID, "staging"); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}

		treeInfo, extractErr = artifact.ExtractFileSafely(archivePath, format, extractedDir, limits)
	} else {
		_ = e.Rollback(ctx, opID)
		return nil, domain.ErrInternal("no artifact or tree source provided for installation", nil)
	}

	if extractErr != nil {
		_ = e.Rollback(ctx, opID)
		return nil, fmt.Errorf("safe extraction failed: %w", extractErr)
	}

	// Step 4: CAS Store Placement & Deduplication
	if err := e.db.AdvanceOperationState(ctx, opID, "commit_intent"); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}

	casTreePath, err := e.TreePath(treeInfo.TreeDigest)
	if err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}

	// Check if tree already exists in CAS store
	if _, statErr := os.Stat(casTreePath); statErr == nil {
		// Tree already exists in CAS! Re-use it and mark created_by_op = 0
		if err := e.db.RecordOperationTree(ctx, opID, treeInfo.TreeDigest, false); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
		_ = os.RemoveAll(stagingDir)
	} else {
		// New tree: atomically place in CAS store and mark created_by_op = 1
		if err := os.MkdirAll(filepath.Dir(casTreePath), 0700); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed to create CAS tree parent dir: %w", err)
		}

		if err := moveOrCopyDir(extractedDir, casTreePath); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, fmt.Errorf("failed to promote tree to CAS store %s: %w", casTreePath, err)
		}

		if err := e.db.RecordOperationTree(ctx, opID, treeInfo.TreeDigest, true); err != nil {
			_ = e.Rollback(ctx, opID)
			return nil, err
		}
		_ = os.RemoveAll(stagingDir)
	}

	// Step 5: Commit metadata to SQLite
	if err := e.db.AdvanceOperationState(ctx, opID, "committing"); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, err
	}

	digestShort := strings.TrimPrefix(treeInfo.TreeDigest, "sha256:")
	if len(digestShort) > 8 {
		digestShort = digestShort[:8]
	}
	installID := fmt.Sprintf("inst_%s_%s", opts.ListingID, digestShort)

	now := time.Now().UTC()
	installRec := &domain.InstallRecord{
		InstallID:   installID,
		ListingID:   opts.ListingID,
		Version:     opts.Version,
		TreeDigest:  treeInfo.TreeDigest,
		Scope:       opts.Scope,
		WorkspaceID: opts.WorkspaceID,
		ProjectRoot: opts.ProjectRoot,
		Status:      domain.InstallActive,
		InstalledAt: now,
		UpdatedAt:   now,
	}

	if err := e.db.SaveInstall(ctx, installRec); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, fmt.Errorf("failed to save install record: %w", err)
	}

	// Save main component record
	compRec := &domain.InstallComponentRecord{
		InstallID:     installID,
		Kind:          domain.ComponentMCPProvider,
		ComponentName: "default",
		Path:          casTreePath,
	}
	if err := e.db.SaveInstallComponent(ctx, compRec); err != nil {
		_ = e.Rollback(ctx, opID)
		return nil, fmt.Errorf("failed to save install component: %w", err)
	}

	// Finalize journal state
	if err := e.db.AdvanceOperationState(ctx, opID, "committed"); err != nil {
		return nil, err
	}

	return installRec, nil
}

// TreePath computes the local filesystem path for a given Merkle tree digest.
func (e *Engine) TreePath(treeDigest string) (string, error) {
	parts := strings.SplitN(treeDigest, ":", 2)
	if len(parts) != 2 || parts[0] != "sha256" || len(parts[1]) != 64 {
		return "", fmt.Errorf("invalid tree digest format %q: expected sha256:<64-hex>", treeDigest)
	}
	for _, c := range parts[1] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return "", fmt.Errorf("invalid hex in tree digest %q", treeDigest)
		}
	}
	return filepath.Join(e.casRoot, "trees", parts[0], parts[1]), nil
}

// Rollback safely reverses an incomplete or failed operation.
// It removes ONLY CAS trees created by this operation (preserving shared pre-existing trees).
func (e *Engine) Rollback(ctx context.Context, opID string) error {
	stagingDir := filepath.Join(e.stagingRoot, opID)
	_ = os.RemoveAll(stagingDir)

	// Clean up newly created CAS trees using state recovery
	err := e.db.RecoverIncompleteOperations(ctx, e.stagingRoot, nil, func(treeDigest string) string {
		path, _ := e.TreePath(treeDigest)
		return path
	})

	_ = e.db.AdvanceOperationState(ctx, opID, "rolled_back")
	return err
}

func moveOrCopyDir(src, dst string) error {
	// Try fast atomic rename first
	if err := os.Rename(src, dst); err == nil {
		return nil
	}

	// Fallback to recursive copy if cross-device link
	if err := os.MkdirAll(dst, 0700); err != nil {
		return err
	}

	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		s := filepath.Join(src, entry.Name())
		d := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := moveOrCopyDir(s, d); err != nil {
				return err
			}
		} else {
			in, err := os.Open(s)
			if err != nil {
				return err
			}
			out, err := os.Create(d)
			if err != nil {
				in.Close()
				return err
			}
			if _, err := io.Copy(out, in); err != nil {
				in.Close()
				out.Close()
				return err
			}
			in.Close()
			out.Close()
		}
	}

	return os.RemoveAll(src)
}
