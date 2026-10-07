// Package inventory captures the observed state of deployed capabilities
// (what LiteSPM owns on disk and in host configs) so reconcile can diff it
// against the desired state from the lockfile. Observation only: this
// package never writes.
package inventory

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// ObservedNode is one deployed node LiteSPM believes it owns.
type ObservedNode struct {
	CapabilityID string    `json:"capabilityId"`
	HostID       string    `json:"hostId"`
	FilePath     string    `json:"filePath"`
	Locator      string    `json:"locator"`
	ImageHash    string    `json:"imageHash"`
	ObservedAt   time.Time `json:"observedAt"`
}

// Snapshot is a point-in-time observed inventory.
type Snapshot struct {
	TakenAt time.Time      `json:"takenAt"`
	Nodes   []ObservedNode `json:"nodes"`
}

// Collector gathers observations. Sources register nodes; collectors never
// invent them.
type Collector struct {
	nodes []ObservedNode
}

// New returns an empty collector.
func New() *Collector { return &Collector{} }

// Observe records one node. Empty capability/file/locator fail closed.
func (c *Collector) Observe(n ObservedNode) error {
	if strings.TrimSpace(n.CapabilityID) == "" || strings.TrimSpace(n.FilePath) == "" || strings.TrimSpace(n.Locator) == "" {
		return fmt.Errorf("LPSM-INVENTORY-NODE: capabilityId, filePath and locator are required")
	}
	if n.ObservedAt.IsZero() {
		n.ObservedAt = time.Now().UTC()
	}
	c.nodes = append(c.nodes, n)
	return nil
}

// Snapshot freezes the current observations in stable order.
func (c *Collector) Snapshot() Snapshot {
	nodes := append([]ObservedNode(nil), c.nodes...)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].FilePath != nodes[j].FilePath {
			return nodes[i].FilePath < nodes[j].FilePath
		}
		return nodes[i].Locator < nodes[j].Locator
	})
	return Snapshot{TakenAt: time.Now().UTC(), Nodes: nodes}
}

// HashImage digests a canonical structure image for comparison.
func HashImage(canonical []byte) string {
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// HashJSON digests the canonical JSON projection of v.
func HashJSON(v any) string {
	b, _ := json.Marshal(v)
	return HashImage(b)
}
