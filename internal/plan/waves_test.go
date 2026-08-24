package plan_test

import (
	"testing"

	"github.com/monrad/takt/internal/plan"
)

func TestAssignWavesFixture(t *testing.T) {
	t.Parallel()
	waves, err := plan.AssignWaves(loadFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]int{1: 0, 2: 0, 3: 0, 8: 0, 4: 1, 5: 1, 7: 1, 6: 2}
	for id, w := range want {
		if waves[id] != w {
			t.Errorf("task %d wave = %d, want %d", id, waves[id], w)
		}
	}
}

func TestAssignWavesCycle(t *testing.T) {
	t.Parallel()
	idx := plan.Index{Schema: 1, Tasks: []plan.Task{
		{ID: 1, DependsOn: []int{2}}, {ID: 2, DependsOn: []int{3}}, {ID: 3, DependsOn: []int{1}}, {ID: 4},
	}}
	if _, err := plan.AssignWaves(idx); err == nil {
		t.Fatal("cycle must be reported")
	}
}
