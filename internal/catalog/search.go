package catalog

import (
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/sarv-projects/litepsm/internal/domain"
)

// SearchOptions provides filtering and pagination for catalog queries.
type SearchOptions struct {
	Kind     domain.ListingKind
	Category string
	Limit    int
	Status   domain.ListingStatus
}

// SearchResult wraps a matched listing with relevance score.
type SearchResult struct {
	Listing       *domain.Listing `json:"listing"`
	Score         float64         `json:"score"`
	MatchedFields []string        `json:"matchedFields"`
}

// SearchIndex provides fast in-memory lexical search with token weighting.
type SearchIndex struct {
	mu       sync.RWMutex
	listings map[string]*domain.Listing // ID -> Listing
}

// NewSearchIndex initializes an empty search index.
func NewSearchIndex() *SearchIndex {
	return &SearchIndex{
		listings: make(map[string]*domain.Listing),
	}
}

// IndexListings bulk indexes or updates listings in memory.
func (idx *SearchIndex) IndexListings(listings []*domain.Listing) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	for _, l := range listings {
		if l != nil && l.ID != "" {
			idx.listings[l.ID] = l
		}
	}
}

// Get retrieves a listing by its canonical ID.
func (idx *SearchIndex) Get(id string) (*domain.Listing, bool) {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	l, ok := idx.listings[id]
	return l, ok
}

// Count returns the total number of indexed listings.
func (idx *SearchIndex) Count() int {
	idx.mu.RLock()
	defer idx.mu.RUnlock()
	return len(idx.listings)
}

// Search performs lexical query matching and ranking.
func (idx *SearchIndex) Search(query string, opts SearchOptions) []*SearchResult {
	idx.mu.RLock()
	defer idx.mu.RUnlock()

	queryNorm := strings.ToLower(strings.TrimSpace(query))
	queryTokens := tokenize(queryNorm)

	var results []*SearchResult

	for _, l := range idx.listings {
		// Filter by Kind if specified
		if opts.Kind != "" && l.Kind != opts.Kind {
			continue
		}

		// Filter by Status (defaults to Active if not specified)
		reqStatus := opts.Status
		if reqStatus == "" {
			reqStatus = domain.ListingStatusActive
		}
		if l.Status != reqStatus {
			continue
		}

		// Filter by Category if specified
		if opts.Category != "" {
			hasCat := false
			catLower := strings.ToLower(opts.Category)
			for _, c := range l.Categories {
				if strings.ToLower(c) == catLower {
					hasCat = true
					break
				}
			}
			if !hasCat {
				continue
			}
		}

		// If query is empty, return all matching filters with baseline score
		if len(queryTokens) == 0 {
			results = append(results, &SearchResult{
				Listing: l,
				Score:   1.0,
			})
			continue
		}

		// Compute relevance score
		score, matchedFields := scoreListing(l, queryNorm, queryTokens)
		if score > 0 {
			results = append(results, &SearchResult{
				Listing:       l,
				Score:         score,
				MatchedFields: matchedFields,
			})
		}
	}

	// Sort descending by Score, then ascending by Name
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score == results[j].Score {
			return results[i].Listing.Name < results[j].Listing.Name
		}
		return results[i].Score > results[j].Score
	})

	limit := opts.Limit
	if limit <= 0 {
		limit = 20
	}
	if len(results) > limit {
		results = results[:limit]
	}

	return results
}

func scoreListing(l *domain.Listing, queryNorm string, queryTokens []string) (float64, []string) {
	var score float64
	var matchedFields []string

	nameLower := strings.ToLower(l.Name)
	titleLower := strings.ToLower(l.Title)
	summaryLower := strings.ToLower(l.Summary)
	idLower := strings.ToLower(l.ID)

	// Exact ID match
	if idLower == queryNorm {
		score += 100.0
		matchedFields = append(matchedFields, "id")
	}

	// Exact Name match
	if nameLower == queryNorm {
		score += 50.0
		matchedFields = append(matchedFields, "name_exact")
	} else if strings.HasPrefix(nameLower, queryNorm) {
		score += 25.0
		matchedFields = append(matchedFields, "name_prefix")
	}

	// Token matches
	for _, token := range queryTokens {
		tokenMatched := false

		if strings.Contains(nameLower, token) {
			score += 10.0
			tokenMatched = true
		}
		if titleLower != "" && strings.Contains(titleLower, token) {
			score += 8.0
			tokenMatched = true
		}
		if summaryLower != "" && strings.Contains(summaryLower, token) {
			score += 4.0
			tokenMatched = true
		}

		for _, cat := range l.Categories {
			if strings.Contains(strings.ToLower(cat), token) {
				score += 6.0
				tokenMatched = true
				break
			}
		}

		for _, kw := range l.Keywords {
			if strings.Contains(strings.ToLower(kw), token) {
				score += 5.0
				tokenMatched = true
				break
			}
		}

		if tokenMatched {
			matchedFields = append(matchedFields, token)
		}
	}

	// Verification bonus
	if score > 0 {
		switch l.VerificationSummary.Level {
		case "security_audited":
			score += 10.0
		case "signature_verified":
			score += 5.0
		}
	}

	return score, matchedFields
}

func tokenize(text string) []string {
	f := func(c rune) bool {
		return !unicode.IsLetter(c) && !unicode.IsNumber(c)
	}
	words := strings.FieldsFunc(text, f)
	var tokens []string
	for _, w := range words {
		if len(w) > 0 {
			tokens = append(tokens, w)
		}
	}
	return tokens
}
