package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sarv-projects/litespm/internal/config"
	"github.com/sarv-projects/litespm/internal/state"
)

// BuildRepairPlan inspects a diagnostic report and generates corrective actions.
func BuildRepairPlan(report *DoctorReport, paths *config.PlatformPaths) *RepairPlan {
	var actions []RepairAction

	for _, c := range report.Checks {
		if c.ID == "check_staging" && c.Status == StatusWarn {
			actions = append(actions, RepairAction{
				ID:          "clean_staging",
				Description: "Purge orphaned files from staging scratch directory",
				ActionKind:  "filesystem_clean",
			})
		}
		if c.ID == "check_dirs" && c.Status == StatusFail {
			actions = append(actions, RepairAction{
				ID:          "repair_dirs",
				Description: "Re-create standard directory hierarchy with restrictive 0700 permissions",
				ActionKind:  "directory_repair",
			})
		}
	}

	return &RepairPlan{
		CreatedAt: time.Now().UTC(),
		Actions:   actions,
	}
}

// ApplyRepairPlan executes all proposed repair actions.
//
// Every action reports its own outcome: Applied is only true when the action
// actually changed something, and failures are recorded on the action and
// collected into the returned error instead of aborting the remaining actions.
// Actions that need the operation journal refuse to run when db is nil rather
// than deleting staging directories that may belong to in-flight operations.
func ApplyRepairPlan(ctx context.Context, plan *RepairPlan, paths *config.PlatformPaths, db *state.DB) error {
	if plan == nil {
		return fmt.Errorf("no repair plan supplied")
	}

	var failures []error
	for i := range plan.Actions {
		action := &plan.Actions[i]
		switch action.ID {
		case "clean_staging":
			if db == nil {
				action.Applied = false
				action.Error = "refusing to clean staging without access to the operation journal (cannot tell active operations from orphans)"
				failures = append(failures, fmt.Errorf("clean_staging: %s", action.Error))
				continue
			}
			if paths == nil {
				action.Applied = false
				action.Error = "no platform paths configured; staging location unknown"
				failures = append(failures, fmt.Errorf("clean_staging: %s", action.Error))
				continue
			}

			active := make(map[string]bool)
			ops, err := db.GetNonTerminalOperations(ctx)
			if err != nil {
				action.Applied = false
				action.Error = fmt.Sprintf("failed to list active operations: %v", err)
				failures = append(failures, fmt.Errorf("clean_staging: %s", action.Error))
				continue
			}
			for _, op := range ops {
				active[op.OperationID] = true
			}

			stagingPath := paths.StagingPath()
			// Same guard as startup recovery: a staging path that exists but
			// is not a directory is a broken root, not "nothing to clean".
			// (On Windows os.ReadDir of a file can report an empty success,
			// which would silently swallow the misconfiguration.)
			if fi, err := os.Lstat(stagingPath); err == nil && !fi.IsDir() {
				action.Applied = false
				action.Error = fmt.Sprintf("staging path %s exists but is not a directory", stagingPath)
				failures = append(failures, fmt.Errorf("clean_staging: %s", action.Error))
				continue
			}
			entries, err := os.ReadDir(stagingPath)
			if err != nil {
				if os.IsNotExist(err) {
					// Nothing to clean; staging does not exist.
					continue
				}
				action.Applied = false
				action.Error = fmt.Sprintf("failed to read staging directory %s: %v", stagingPath, err)
				failures = append(failures, fmt.Errorf("clean_staging: %s", action.Error))
				continue
			}

			removed := 0
			skippedActive := 0
			var removeErr error
			for _, entry := range entries {
				if active[entry.Name()] {
					skippedActive++
					continue
				}
				if err := os.RemoveAll(filepath.Join(stagingPath, entry.Name())); err != nil {
					removeErr = fmt.Errorf("failed to remove staging entry %s: %w", entry.Name(), err)
					break
				}
				removed++
			}
			if removeErr != nil {
				action.Applied = removed > 0
				action.Error = removeErr.Error()
				failures = append(failures, fmt.Errorf("clean_staging: %w", removeErr))
				continue
			}
			action.Applied = removed > 0
			if skippedActive > 0 && removed == 0 {
				action.Error = fmt.Sprintf("nothing removed; all %d staging entries belong to active operations", skippedActive)
			}

		case "repair_dirs":
			if paths == nil {
				action.Applied = false
				action.Error = "no platform paths configured; directory hierarchy location unknown"
				failures = append(failures, fmt.Errorf("repair_dirs: %s", action.Error))
				continue
			}
			if err := paths.EnsureDirectories(); err != nil {
				action.Applied = false
				action.Error = err.Error()
				failures = append(failures, fmt.Errorf("repair_dirs: %w", err))
				continue
			}
			action.Applied = true

		default:
			action.Applied = false
			action.Error = fmt.Sprintf("unknown repair action %q", action.ID)
			failures = append(failures, fmt.Errorf("repair: %s", action.Error))
		}
	}
	return errors.Join(failures...)
}
