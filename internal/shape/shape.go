// Package shape describes the structure of a JSON payload and what changed between two of them.
//
//	╔══════════════════════════════════════════════════════════════════════╗
//	║  LEVEL 3 · WHAT CHANGED                                 owner: @human║
//	║                                                                      ║
//	║  Write Of and Diff below. Unlocks:                                   ║
//	║   • the "What changed since the last one that worked" panel          ║
//	║   • sharper diagnoses: the model is told exactly which field moved   ║
//	║                                                                      ║
//	║  Check your progress:  go run ./cmd/unlock                           ║
//	║  Task: specs/001-holdfast-mvp/tasks.md → H3                          ║
//	╚══════════════════════════════════════════════════════════════════════╝
//
// The relay compares a failed event with the last event of the same type that the app accepted. A
// field that was renamed, removed or changed type is the most common reason an integration breaks
// overnight, and this finds it without needing the provider's API spec.
package shape

import (
	"encoding/json"
	"errors"
	"sort"
)

// Kind is the JSON type at one path.
type Kind string

const (
	Object Kind = "object"
	Array  Kind = "array"
	String Kind = "string"
	Number Kind = "number"
	Bool   Kind = "bool"
	Null   Kind = "null"
	Mixed  Kind = "mixed" // array items disagree on the type at this path
)

// Shape maps every path in a document to its kind.
type Shape map[string]Kind

// ErrNotJSON is returned by Of when the body is not valid JSON.
var ErrNotJSON = errors.New("body is not valid JSON")

// Of returns the shape of a JSON document.
//
// Paths:
//   - the root is "$"
//   - an object key k under path p is p + "." + k   ("$.data.object.amount")
//   - items of an array at path p live under p + "[]" ("$.items[].sku")
//
// Every path that exists gets an entry, including objects and arrays themselves.
// Array items are merged: if items disagree on the kind at the same path, that path is Mixed.
// An empty array has an entry for itself but nothing under "[]".
//
// Example: {"id":"evt_1","items":[{"sku":"A"}]} →
//
//	"$": object, "$.id": string, "$.items": array, "$.items[]": object, "$.items[].sku": string
//
// Return ErrNotJSON (wrapped is fine) for anything that does not parse.
// Hint: json.Unmarshal into `any` gives you map[string]any, []any, string, float64, bool and nil.
func Of(body []byte) (Shape, error) {
	var doc any
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, ErrNotJSON
	}

	s := Shape{}
	walk(s, "$", doc) // start at the outside; walk works its way in
	return s, nil
}

// walk records the kind of v at path, then does the same for everything inside v.
// It is the folder-listing idea: write down what this thing is, and if it contains
// other things, call walk again for each of them with a longer path.
func walk(s Shape, path string, v any) {
	switch v := v.(type) {
	case map[string]any: // an object: record it, then visit every key
		record(s, path, Object)
		for key, inner := range v {
			walk(s, path+"."+key, inner)
		}
	case []any: // an array: record it, then visit every item; all items share path+"[]"
		record(s, path, Array)
		for _, item := range v {
			walk(s, path+"[]", item)
		}
	case string: // the four plain kinds hold nothing inside, so just record and stop
		record(s, path, String)
	case float64:
		record(s, path, Number)
	case bool:
		record(s, path, Bool)
	case nil:
		record(s, path, Null)
	}
}

// record writes one entry. Array items share a path, so if a different kind is already
// there, the items disagree and the path becomes Mixed.
func record(s Shape, path string, k Kind) {
	if old, seen := s[path]; seen && old != k {
		k = Mixed
	}
	s[path] = k
}

// ChangeKind says how one path differs.
type ChangeKind string

const (
	Added   ChangeKind = "added"   // in bad, not in good
	Removed ChangeKind = "removed" // in good, not in bad
	Retyped ChangeKind = "retyped" // in both, different kind
)

// Change is one difference between two shapes.
type Change struct {
	Path string
	Kind ChangeKind
	Was  Kind // "" for Added
	Now  Kind // "" for Removed
}

// Diff lists what changed from good (the last event the app accepted) to bad (the one it rejected),
// sorted by Path. Identical shapes give an empty slice.
//
// A renamed field shows up as one Removed and one or more Added paths, for example
// customer_id → customer.id gives:
//
//	{"$.data.customer", Added, "", object}
//	{"$.data.customer.id", Added, "", string}
//	{"$.data.customer_id", Removed, string, ""}
func Diff(good, bad Shape) ([]Change, error) {
	var changes []Change
	// Everything the good one had: is it gone, or a different kind now?
	for path, was := range good {
		now, still := bad[path]
		switch {
		case !still:
			changes = append(changes, Change{Path: path, Kind: Removed, Was: was})
		case now != was:
			changes = append(changes, Change{Path: path, Kind: Retyped, Was: was, Now: now})
		}
	}
	// Everything the bad one has that the good one never had.
	for path, now := range bad {
		if _, had := good[path]; !had {
			changes = append(changes, Change{Path: path, Kind: Added, Now: now})
		}
	}
	// Map order is random in Go, so sort to give the same answer every time.
	sort.Slice(changes, func(i, j int) bool { return changes[i].Path < changes[j].Path })
	return changes, nil
}
