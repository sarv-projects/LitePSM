package doctor

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sarv-projects/litepsm/internal/config"
	"github.com/sarv-projects/litepsm/internal/state"
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
func ApplyRepairPlan(ctx context.Context, plan *RepairPlan, paths *config.PlatformPaths, db *state.DB) error {
	for i := range plan.Actions {
		action := &plan.Actions[i]
		switch action.ID {
		case "clean_staging":
			if paths != nil {
				stagingPath := paths.StagingPath()
				entries, err := os.ReadDir(stagingPath)
				if err == nil {
					for _, entry := range entries {
						_ = os.RemoveAll(stagingPath + "/" + entry.Name())
					}
				}
				action.Applied = true
			}

		case "repair_dirs":
			if paths != nil {
				err := paths.EnsureDirectories()
				if err != nil {
					action.Error = err.Error()
					return fmt.Errorf("failed to repair directories: %w", err)
				}
				action.Applied = true
			}
		}
	}
	return nil
}
