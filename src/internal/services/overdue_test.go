package services

import (
	"fmt"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestListOverdue_FilteringAndOrdering(t *testing.T) {
	now := baseTime.Add(48 * time.Hour)
	s := newEmptyService()
	s.now = fixedClock(now)

	seedTask(s, "late3", models.TaskStatusPending, timePtr(now.Add(-1*time.Hour)), 1)
	seedTask(s, "late1", models.TaskStatusInProgress, timePtr(now.Add(-72*time.Hour)), 2)
	seedTask(s, "late2", models.TaskStatusOnHold, timePtr(now.Add(-24*time.Hour)), 3)
	seedTask(s, "future", models.TaskStatusPending, timePtr(now.Add(time.Hour)), 4)
	seedTask(s, "nodue", models.TaskStatusPending, nil, 5)
	seedTask(s, "done", models.TaskStatusCompleted, timePtr(now.Add(-24*time.Hour)), 6)
	seedTask(s, "cancelled", models.TaskStatusCancelled, timePtr(now.Add(-24*time.Hour)), 7)

	resp, err := s.ListOverdueTasks(models.ListTasksQuery{})
	if err != nil {
		t.Fatal(err)
	}
	got := listIDs(resp)
	want := []string{"late1", "late2", "late3"}
	if fmt.Sprint(got) != fmt.Sprint(want) || resp.TotalCount != 3 || resp.NextPageToken != "" {
		t.Fatalf("got %v total=%d next=%q", got, resp.TotalCount, resp.NextPageToken)
	}
}

func TestListOverdue_NilDueDateExcluded(t *testing.T) {
	s := newEmptyService()
	seedTask(s, "a", models.TaskStatusPending, nil, 0)
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	if resp.TotalCount != 0 || len(resp.Tasks) != 0 {
		t.Fatalf("got %+v", resp)
	}
}

func TestListOverdue_BoundaryIsStrictlyBefore(t *testing.T) {
	now := baseTime
	s := newEmptyService()
	s.now = fixedClock(now)
	seedTask(s, "exact", models.TaskStatusPending, timePtr(now), 0)
	seedTask(s, "just_before", models.TaskStatusPending, timePtr(now.Add(-time.Nanosecond)), 1)
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	if got := listIDs(resp); len(got) != 1 || got[0] != "just_before" {
		t.Fatalf("got %v", got)
	}
}

func TestListOverdue_TieBreakByCreatedAtThenID(t *testing.T) {
	now := baseTime.Add(time.Hour)
	due := timePtr(baseTime)
	s := newEmptyService()
	s.now = fixedClock(now)
	seedTask(s, "c", models.TaskStatusPending, due, 5)
	seedTask(s, "b", models.TaskStatusPending, due, 5)
	seedTask(s, "a", models.TaskStatusPending, due, 9)
	seedTask(s, "z", models.TaskStatusPending, due, 1)
	for i := 0; i < 20; i++ { // map iteration order is random; result must be stable
		resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
		if got := fmt.Sprint(listIDs(resp)); got != "[z b c a]" {
			t.Fatalf("got %s", got)
		}
	}
}

func TestListOverdue_PaginationAcrossPages(t *testing.T) {
	now := baseTime.Add(1000 * time.Hour)
	s := newEmptyService()
	s.now = fixedClock(now)
	for i := 0; i < 7; i++ {
		seedTask(s, fmt.Sprintf("o%02d", i), models.TaskStatusPending, timePtr(baseTime.Add(time.Duration(i)*time.Hour)), i)
	}
	seedTask(s, "future", models.TaskStatusPending, timePtr(now.Add(time.Hour)), 50)

	var all []string
	token := ""
	for pages := 0; pages < 10; pages++ {
		resp, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 3, PageToken: token})
		if resp.TotalCount != 7 {
			t.Fatalf("total %d", resp.TotalCount)
		}
		all = append(all, listIDs(resp)...)
		if resp.NextPageToken == "" {
			break
		}
		token = resp.NextPageToken
	}
	if fmt.Sprint(all) != "[o00 o01 o02 o03 o04 o05 o06]" {
		t.Fatalf("got %v", all)
	}
	// default page size and clamp behave like list
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{PageSize: 500})
	if len(resp.Tasks) != 7 {
		t.Fatalf("got %d", len(resp.Tasks))
	}
}

func TestClockInjection(t *testing.T) {
	s := newEmptyService()
	due := baseTime
	seedTask(s, "a", models.TaskStatusPending, &due, 0)

	s.now = fixedClock(baseTime.Add(-time.Hour))
	if resp, _ := s.ListOverdueTasks(models.ListTasksQuery{}); resp.TotalCount != 0 {
		t.Fatalf("not yet overdue, got %d", resp.TotalCount)
	}
	s.now = fixedClock(baseTime.Add(time.Hour))
	if resp, _ := s.ListOverdueTasks(models.ListTasksQuery{}); resp.TotalCount != 1 {
		t.Fatalf("overdue, got %d", resp.TotalCount)
	}
}

func TestWithClockOption(t *testing.T) {
	fixed := baseTime.Add(5 * time.Hour)
	s := NewTaskService(WithClock(fixedClock(fixed)))
	task, err := s.CreateTask(models.CreateTaskRequest{Title: "x"})
	if err != nil || !task.CreatedAt.Equal(fixed) {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	// nil options are ignored, defaults stay usable
	d := NewTaskService(WithClock(nil), WithNotifier(nil))
	if d.now == nil || d.notifier == nil {
		t.Fatal("defaults lost")
	}
}

func TestListOverdue_ReturnsCopies(t *testing.T) {
	s := newEmptyService()
	s.now = fixedClock(baseTime.Add(time.Hour))
	seedTask(s, "a", models.TaskStatusPending, timePtr(baseTime), 0)
	resp, _ := s.ListOverdueTasks(models.ListTasksQuery{})
	resp.Tasks[0].Title = "mutated"
	if s.tasks["a"].Title == "mutated" {
		t.Fatal("response aliases stored task")
	}
}
