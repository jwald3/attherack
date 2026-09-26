// Package exercise is the exercise library: the embedded free-exercise-db
// dataset plus user-added movements, with search and facet counts.
package exercise

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// builtinJSON is free-exercise-db (yuhonas/free-exercise-db, MIT).
//
//go:embed exercises.json
var builtinJSON []byte

// Exercise mirrors the free-exercise-db (yuhonas/free-exercise-db, MIT) schema.
type Exercise struct {
	Name             string   `json:"name"`
	Force            *string  `json:"force"`
	Level            string   `json:"level"`
	Mechanic         *string  `json:"mechanic"`
	Equipment        *string  `json:"equipment"`
	PrimaryMuscles   []string `json:"primaryMuscles"`
	SecondaryMuscles []string `json:"secondaryMuscles"`
	Instructions     []string `json:"instructions"`
	Category         string   `json:"category"`
	Images           []string `json:"images"`
	ID               string   `json:"id"`

	// Custom is true for user-added exercises (not part of free-exercise-db).
	Custom bool `json:"custom,omitempty"`
}

// EquipmentName returns the equipment, or "" when the dataset has none.
func (e Exercise) EquipmentName() string {
	if e.Equipment == nil {
		return ""
	}
	return *e.Equipment
}

// Library holds the embedded dataset plus any user-added exercises and
// supports lightweight search. Safe for concurrent Search/Add.
type Library struct {
	mu   sync.RWMutex
	all  []Exercise
	byID map[string]Exercise
}

// Add merges runtime (custom) exercises into the library so they appear in
// search results immediately, without a restart.
func (l *Library) Add(exs ...Exercise) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range exs {
		if _, exists := l.byID[e.ID]; exists {
			continue
		}
		l.all = append(l.all, e)
		l.byID[e.ID] = e
	}
}

// Count returns the number of exercises currently in the library.
func (l *Library) Count() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.all)
}

// Facets are the distinct filterable values in the library, each with a count,
// ordered most-common first. Used to render suggestion chips.
type Facet struct {
	Value string
	Count int
}

// Facets returns the distinct primary muscles and equipment across the library,
// most common first. Recomputed on demand so custom exercises are included.
func (l *Library) Facets() (muscles, equipment []Facet) {
	l.mu.RLock()
	defer l.mu.RUnlock()

	mCount := map[string]int{}
	eCount := map[string]int{}
	for _, e := range l.all {
		for _, m := range e.PrimaryMuscles {
			m = strings.TrimSpace(m)
			if m != "" {
				mCount[m]++
			}
		}
		if e.Equipment != nil {
			eq := strings.TrimSpace(*e.Equipment)
			if eq != "" {
				eCount[eq]++
			}
		}
	}
	return rankFacets(mCount), rankFacets(eCount)
}

func rankFacets(counts map[string]int) []Facet {
	out := make([]Facet, 0, len(counts))
	for v, n := range counts {
		out = append(out, Facet{Value: v, Count: n})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out
}

// LoadBuiltin returns a library of the embedded dataset.
func LoadBuiltin() (*Library, error) {
	return Load(builtinJSON)
}

// Load builds a library from free-exercise-db JSON.
func Load(raw []byte) (*Library, error) {
	var all []Exercise
	if err := json.Unmarshal(raw, &all); err != nil {
		return nil, err
	}
	lib := &Library{all: all, byID: make(map[string]Exercise, len(all))}
	for _, e := range all {
		lib.byID[e.ID] = e
	}
	return lib, nil
}

// Search returns exercises matching a free-text query, ranked by relevance.
// Optional filters (muscle, equipment) narrow the results; empty means "any".
func (l *Library) Search(query, muscle, equipment string, limit int) []Exercise {
	if limit <= 0 {
		limit = 25
	}
	q := strings.ToLower(strings.TrimSpace(query))
	muscle = strings.ToLower(strings.TrimSpace(muscle))
	equipment = strings.ToLower(strings.TrimSpace(equipment))

	l.mu.RLock()
	defer l.mu.RUnlock()

	type scored struct {
		ex    Exercise
		score int
	}
	var hits []scored
	for _, e := range l.all {
		if muscle != "" && !containsFold(e.PrimaryMuscles, muscle) && !containsFold(e.SecondaryMuscles, muscle) {
			continue
		}
		if equipment != "" {
			if e.Equipment == nil || !strings.Contains(strings.ToLower(*e.Equipment), equipment) {
				continue
			}
		}
		score := 1 // baseline for passing the filters
		if q != "" {
			name := strings.ToLower(e.Name)
			switch {
			case name == q:
				score += 100
			case strings.HasPrefix(name, q):
				score += 50
			case strings.Contains(name, q):
				score += 25
			case containsFold(e.PrimaryMuscles, q):
				score += 10
			default:
				continue // query given but nothing matched
			}
		}
		if e.Custom {
			score += 5 // surface the user's own movements a little higher
		}
		hits = append(hits, scored{e, score})
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].ex.Name < hits[j].ex.Name
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]Exercise, len(hits))
	for i, h := range hits {
		out[i] = h.ex
	}
	return out
}

// Get finds an exercise by id.
func (l *Library) Get(id string) (Exercise, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	e, ok := l.byID[id]
	return e, ok
}

// ByName finds an exercise by case-insensitive name match.
func (l *Library) ByName(name string) (Exercise, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	name = strings.ToLower(strings.TrimSpace(name))
	for _, e := range l.all {
		if strings.ToLower(e.Name) == name {
			return e, true
		}
	}
	return Exercise{}, false
}

func containsFold(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(strings.ToLower(h), needle) {
			return true
		}
	}
	return false
}
