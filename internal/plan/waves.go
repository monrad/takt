package plan

import (
	"fmt"
	"sort"
)

// AssignWaves computes wave(t) = 0 for tasks without dependencies, else
// 1 + max(wave(dep)) — Kahn's algorithm over depends_on (spec §7.3).
// Returns an error naming the tasks left in a cycle.
func AssignWaves(idx Index) (map[int]int, error) {
	indeg, children := dependencyGraph(idx)
	waves := map[int]int{}
	var queue []int
	for _, t := range idx.Tasks {
		if indeg[t.ID] == 0 {
			queue = append(queue, t.ID)
			waves[t.ID] = 0
		}
	}
	sort.Ints(queue)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, c := range children[id] {
			if waves[id]+1 > waves[c] {
				waves[c] = waves[id] + 1
			}
			indeg[c]--
			if indeg[c] == 0 {
				queue = append(queue, c)
			}
		}
	}
	if len(waves) != len(idx.Tasks) {
		return nil, cycleError(idx, waves)
	}
	return waves, nil
}

// dependencyGraph returns each task's in-degree and its children (the tasks
// that depend on it directly), the adjacency Kahn's algorithm walks.
func dependencyGraph(idx Index) (map[int]int, map[int][]int) {
	indeg := map[int]int{}
	children := map[int][]int{}
	for _, t := range idx.Tasks {
		indeg[t.ID] += 0
		for _, d := range t.DependsOn {
			indeg[t.ID]++
			children[d] = append(children[d], t.ID)
		}
	}
	return indeg, children
}

// cycleError names the tasks Kahn's algorithm never reached — the
// dependency cycle.
func cycleError(idx Index, waves map[int]int) error {
	var stuck []int
	for _, t := range idx.Tasks {
		if _, ok := waves[t.ID]; !ok {
			stuck = append(stuck, t.ID)
		}
	}
	sort.Ints(stuck)
	return fmt.Errorf("dependency cycle among tasks %v", stuck)
}
