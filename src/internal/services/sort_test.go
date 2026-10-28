package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

func TestSortTasks(t *testing.T) {
	s := newEmptyService()
	d1, d2, d3 := baseTime.Add(1*time.Hour), baseTime.Add(2*time.Hour), baseTime.Add(3*time.Hour)

	// base order (created_at, id): a, b, c, d, e, f
	a := seedTask(s, "a", models.TaskStatusPending, &d2, 0)
	b := seedTask(s, "b", models.TaskStatusPending, nil, 0) // created tie with a
	c := seedTask(s, "c", models.TaskStatusPending, &d1, 1)
	d := seedTask(s, "d", models.TaskStatusPending, &d3, 2)
	e := seedTask(s, "e", models.TaskStatusPending, nil, 3)
	f := seedTask(s, "f", models.TaskStatusPending, &d1, 4)
	a.Priority = models.TaskPriorityHigh
	b.Priority = models.TaskPriorityLow
	c.Priority = models.TaskPriorityCritical
	d.Priority = models.TaskPriorityHigh
	e.Priority = models.TaskPriorityMedium
	f.Priority = models.TaskPriorityLow

	cases := []struct {
		order models.SortOrder
		want  string
	}{
		{"", "[a b c d e f]"},
		{models.SortOrderCreatedAtAsc, "[a b c d e f]"},
		{models.SortOrderCreatedAtDesc, "[f e d c a b]"},
		{models.SortOrderDueDateAsc, "[c f a d b e]"},
		{models.SortOrderDueDateDesc, "[d a c f b e]"},
		{models.SortOrderPriorityAsc, "[b f e a d c]"},
		{models.SortOrderPriorityDesc, "[c a d e b f]"},
	}

	perms := [][]*models.Task{
		{a, b, c, d, e, f},
		{f, e, d, c, b, a},
		{c, a, f, b, e, d},
	}
	for _, tc := range cases {
		for i, p := range perms {
			tasks := append([]*models.Task(nil), p...)
			s.sortTasks(tasks, tc.order)
			ids := make([]string, len(tasks))
			for j, x := range tasks {
				ids[j] = x.ID
			}
			if got := fmt.Sprint(ids); got != tc.want {
				t.Errorf("order %q perm %d: got %s want %s", tc.order, i, got, tc.want)
			}
		}
	}
}
