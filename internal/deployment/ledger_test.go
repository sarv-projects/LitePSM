package deployment_test

import (
	"testing"

	"github.com/sarv-projects/litespm/internal/deployment"
)

func TestReconcileDecisionTable(t *testing.T) {
	m := deployment.Mutation{PreImageHash: "sha256:aaa", PostImageHash: "sha256:bbb"}
	if r := deployment.Reconcile(m, "sha256:bbb"); r.Outcome != deployment.ReconcileUnmodified {
		t.Errorf("C==B should be unmodified, got %s", r.Outcome)
	}
	if r := deployment.Reconcile(m, "sha256:aaa"); r.Outcome != deployment.ReconcileUnmodified {
		t.Errorf("C==A should be unmodified, got %s", r.Outcome)
	}
	if r := deployment.Reconcile(m, "sha256:ccc"); r.Outcome != deployment.ReconcileUserEdited {
		t.Errorf("divergent C should be user_edited, got %s", r.Outcome)
	}
	if r := deployment.Reconcile(m, ""); r.Outcome != deployment.ReconcileMissing {
		t.Errorf("missing C should be missing, got %s", r.Outcome)
	}
}
