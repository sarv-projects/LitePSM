package resolver

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// ListingVersionMetadata holds version information and transitive dependencies of a listing.
type ListingVersionMetadata struct {
	Version      string
	Dependencies []domain.DependencyConstraint
}

// ListingProvider provides catalog metadata needed for dependency resolution.
type ListingProvider interface {
	GetListing(ctx context.Context, listingID string) (*domain.Listing, error)
	GetListingVersions(ctx context.Context, listingID string) ([]ListingVersionMetadata, error)
}

// Resolver resolves listing dependency graphs with constraint intersection and cycle detection.
type Resolver struct {
	provider ListingProvider
}

// NewResolver creates a new Resolver using the supplied ListingProvider.
func NewResolver(provider ListingProvider) *Resolver {
	return &Resolver{provider: provider}
}

// Resolve executes two-phase constraint intersection and topological dependency ordering.
func (r *Resolver) Resolve(ctx context.Context, rootListingID string, rootConstraintStr string) (*domain.DependencyResolutionResult, error) {
	if strings.TrimSpace(rootListingID) == "" {
		return nil, domain.ErrResolveConflict("", "root listing id cannot be empty")
	}

	if rootConstraintStr == "" {
		rootConstraintStr = "*"
	}

	rootConstraint, err := ParseConstraint(rootConstraintStr)
	if err != nil {
		return nil, fmt.Errorf("invalid root constraint: %w", err)
	}

	// Step 1: Discover all required nodes, aggregate constraints, and detect cycles
	constraints := make(map[string]Constraint)
	constraints[rootListingID] = rootConstraint

	graph := make(map[string][]string) // parent -> dependencies
	selectedVersions := make(map[string]string)
	nodes := make(map[string]domain.DependencyResolution)
	visited := make(map[string]bool)
	visiting := make(map[string]bool)
	var visitPath []string

	// Recursive discovery and constraint intersection
	var explore func(id string, c Constraint, depth int, isDirect bool) error
	explore = func(id string, c Constraint, depth int, isDirect bool) error {
		if visiting[id] {
			// Cycle detected! Build the cycle path
			cycleIdx := 0
			for i, p := range visitPath {
				if p == id {
					cycleIdx = i
					break
				}
			}
			cycleChain := append(visitPath[cycleIdx:], id)
			return domain.ErrResolveCycle(strings.Join(cycleChain, " -> "))
		}

		if existing, ok := constraints[id]; ok {
			constraints[id] = existing.Intersect(c)
		} else {
			constraints[id] = c
		}

		if visited[id] {
			return nil
		}

		visiting[id] = true
		visitPath = append(visitPath, id)

		// Fetch available versions for this listing
		availVersions, err := r.provider.GetListingVersions(ctx, id)
		if err != nil {
			return fmt.Errorf("failed to fetch versions for listing %s: %w", id, err)
		}
		if len(availVersions) == 0 {
			return domain.ErrResolveConflict(id, "no versions available in catalog")
		}

		// Sort available versions descending (highest version first)
		type parsedVerMeta struct {
			meta ListingVersionMetadata
			ver  Version
		}
		var parsedList []parsedVerMeta
		for _, v := range availVersions {
			pv, err := ParseVersion(v.Version)
			if err == nil {
				parsedList = append(parsedList, parsedVerMeta{meta: v, ver: pv})
			}
		}

		sort.Slice(parsedList, func(i, j int) bool {
			return parsedList[i].ver.Compare(parsedList[j].ver) > 0
		})

		// Find the best version matching aggregated constraint
		effectiveConstraint := constraints[id]
		var matchedMeta *ListingVersionMetadata
		for _, p := range parsedList {
			if effectiveConstraint.Matches(p.ver) {
				matchedMeta = &p.meta
				break
			}
		}

		if matchedMeta == nil {
			return domain.ErrResolveConflict(id, fmt.Sprintf("unresolvable version conflict with constraint %q", effectiveConstraint.Raw))
		}

		selectedVersions[id] = matchedMeta.Version
		nodes[id] = domain.DependencyResolution{
			ListingID:       id,
			SelectedVersion: matchedMeta.Version,
			Direct:          isDirect,
			Depth:           depth,
		}

		// Process dependencies of this selected version
		var depIDs []string
		for _, dep := range matchedMeta.Dependencies {
			depIDs = append(depIDs, dep.ListingID)
			depConstraint, err := ParseConstraint(dep.Constraint)
			if err != nil {
				return fmt.Errorf("invalid dependency constraint %q for %s: %w", dep.Constraint, dep.ListingID, err)
			}
			if err := explore(dep.ListingID, depConstraint, depth+1, false); err != nil {
				return err
			}
		}
		graph[id] = depIDs

		visiting[id] = false
		visitPath = visitPath[:len(visitPath)-1]
		visited[id] = true
		return nil
	}

	if err := explore(rootListingID, rootConstraint, 0, true); err != nil {
		return nil, err
	}

	// Step 2: Validate consistency of all selected versions against final intersected constraints
	for id, verStr := range selectedVersions {
		v, err := ParseVersion(verStr)
		if err != nil {
			return nil, fmt.Errorf("corrupt selected version %s for %s: %w", verStr, id, err)
		}
		if !constraints[id].Matches(v) {
			return nil, domain.ErrResolveConflict(id, fmt.Sprintf("diamond dependency conflict: selected %s does not satisfy intersected constraint %q", verStr, constraints[id].Raw))
		}
	}

	// Step 3: Compute deterministic Topological Sort (dependencies before dependents)
	topologicalOrder, err := computeTopologicalOrder(rootListingID, graph)
	if err != nil {
		return nil, err
	}

	// Convert constraints map to string map
	constraintMap := make(map[string]string)
	for k, v := range constraints {
		constraintMap[k] = v.Raw
	}

	return &domain.DependencyResolutionResult{
		RootListingID:    rootListingID,
		SelectedVersions: selectedVersions,
		TopologicalOrder: topologicalOrder,
		ResolvedRanges:   constraintMap,
		Nodes:            nodes,
	}, nil
}

func computeTopologicalOrder(root string, graph map[string][]string) ([]string, error) {
	var result []string
	visited := make(map[string]bool)
	tempMark := make(map[string]bool)

	var dfs func(node string) error
	dfs = func(node string) error {
		if tempMark[node] {
			return domain.ErrResolveCycle(fmt.Sprintf("cycle in topological sorting: %s", node))
		}
		if visited[node] {
			return nil
		}

		tempMark[node] = true

		// Sort outgoing dependencies deterministically by name
		deps := append([]string{}, graph[node]...)
		sort.Strings(deps)

		for _, dep := range deps {
			if err := dfs(dep); err != nil {
				return err
			}
		}

		tempMark[node] = false
		visited[node] = true
		result = append(result, node)
		return nil
	}

	if err := dfs(root); err != nil {
		return nil, err
	}

	return result, nil
}
