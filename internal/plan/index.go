// Package plan defines plan.index.json (spec §7.3): the task schema the
// planner writes, its validation rules, and deterministic wave assignment.
package plan

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// Task is one planned unit of work.
type Task struct {
	ID          int      `json:"id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Files       []string `json:"files"`
	Verify      []string `json:"verify"`
	DependsOn   []int    `json:"depends_on"`
	Goals       []string `json:"goals"`
	Class       string   `json:"class"`
	Wave        *int     `json:"wave,omitempty"` // display only; takt assigns it
}

// Index is the whole plan.index.json.
type Index struct {
	Schema   int    `json:"schema"`
	SpecHash string `json:"spec_hash"`
	Tasks    []Task `json:"tasks"`
}

// DefaultClass is assumed when a task omits class.
const DefaultClass = "implement"

// ParseIndex decodes strictly, defaults class, and sorts tasks by id.
func ParseIndex(b []byte) (Index, error) {
	var idx Index
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&idx); err != nil {
		return Index{}, fmt.Errorf("plan.index.json: %w", err)
	}
	for i := range idx.Tasks {
		if idx.Tasks[i].Class == "" {
			idx.Tasks[i].Class = DefaultClass
		}
		if idx.Tasks[i].DependsOn == nil {
			idx.Tasks[i].DependsOn = []int{}
		}
		if idx.Tasks[i].Goals == nil {
			idx.Tasks[i].Goals = []string{}
		}
	}
	sort.SliceStable(idx.Tasks, func(i, j int) bool { return idx.Tasks[i].ID < idx.Tasks[j].ID })
	return idx, nil
}

// Task returns the task with id, or nil.
func (idx Index) Task(id int) *Task {
	for i := range idx.Tasks {
		if idx.Tasks[i].ID == id {
			return &idx.Tasks[i]
		}
	}
	return nil
}

// Canonical returns stable bytes with the display-only wave removed (spec §9).
func Canonical(idx Index) ([]byte, error) {
	c := Index{Schema: idx.Schema, SpecHash: idx.SpecHash, Tasks: make([]Task, len(idx.Tasks))}
	copy(c.Tasks, idx.Tasks)
	sort.SliceStable(c.Tasks, func(i, j int) bool { return c.Tasks[i].ID < c.Tasks[j].ID })
	for i := range c.Tasks {
		c.Tasks[i].Wave = nil
	}
	return json.Marshal(c)
}
