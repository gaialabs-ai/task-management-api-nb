package services

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

func strPtr(v string) *string { return &v }

func TestNewTaskServiceSeedsSampleData(t *testing.T) {
	s := NewTaskService()
	resp, err := s.ListTasks(models.ListTasksQuery{PageSize: 100})
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalCount != 2 || len(resp.Tasks) != 2 {
		t.Fatalf("expected 2 sample tasks, got total=%d len=%d", resp.TotalCount, len(resp.Tasks))
	}
	for _, task := range resp.Tasks {
		if task.ID == "" || task.Title == "" || task.Status != models.TaskStatusPending || task.CreatedBy != "system" {
			t.Errorf("unexpected sample task: %+v", task)
		}
	}
}

func TestCreateTask(t *testing.T) {
	due := fixedNow.Add(time.Hour)
	tests := []struct {
		name     string
		req      models.CreateTaskRequest
		wantErr  string
		priority models.TaskPriority
	}{
		{"defaults priority to medium", models.CreateTaskRequest{Title: "a"}, "", models.TaskPriorityMedium},
		{"keeps explicit priority", models.CreateTaskRequest{Title: "a", Priority: models.TaskPriorityCritical}, "", models.TaskPriorityCritical},
		{"rejects invalid priority", models.CreateTaskRequest{Title: "a", Priority: "urgent"}, "invalid priority: urgent", ""},
		{"keeps due date and assignee", models.CreateTaskRequest{Title: "a", DueDate: &due, AssignedTo: strPtr("x@y.com")}, "", models.TaskPriorityMedium},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestService()
			task, err := s.CreateTask(tt.req)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				if len(s.tasks) != 0 {
					t.Error("invalid task must not be stored")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if task.ID == "" || task.Status != models.TaskStatusPending || task.CreatedBy != "system" ||
				task.Priority != tt.priority || task.Tags == nil || task.CompletedAt != nil ||
				task.CreatedAt.IsZero() || !task.CreatedAt.Equal(task.UpdatedAt) {
				t.Errorf("unexpected task: %+v", task)
			}
			if tt.req.DueDate != nil && (task.DueDate == nil || !task.DueDate.Equal(due)) {
				t.Errorf("due date lost: %+v", task)
			}
			if tt.req.AssignedTo != nil && (task.AssignedTo == nil || *task.AssignedTo != "x@y.com") {
				t.Errorf("assignee lost: %+v", task)
			}
			if got, err := s.GetTask(task.ID); err != nil || got.ID != task.ID {
				t.Errorf("created task not retrievable: %v", err)
			}
		})
	}
}

func TestCreateTaskGeneratesUniqueIDs(t *testing.T) {
	s := newTestService()
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		task, err := s.CreateTask(models.CreateTaskRequest{Title: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if seen[task.ID] {
			t.Fatalf("duplicate ID %s", task.ID)
		}
		seen[task.ID] = true
	}
}

func TestCreateTaskKeepsProvidedTags(t *testing.T) {
	s := newTestService()
	task, _ := s.CreateTask(models.CreateTaskRequest{Title: "x", Tags: []string{"a", "b"}})
	if len(task.Tags) != 2 || task.Tags[0] != "a" {
		t.Errorf("tags = %v", task.Tags)
	}
}

func TestGetTask(t *testing.T) {
	s := newTestService()
	addTask(s, "t1", models.TaskStatusPending, nil)

	got, err := s.GetTask("t1")
	if err != nil || got.ID != "t1" {
		t.Fatalf("GetTask = %+v, %v", got, err)
	}

	got, err = s.GetTask("missing")
	if got != nil || err == nil || err.Error() != "task with ID missing not found" {
		t.Errorf("GetTask(missing) = %+v, %v", got, err)
	}
	if _, err := s.GetTask(""); err == nil {
		t.Error("empty ID must not be found")
	}
}

func TestUpdateTask(t *testing.T) {
	newTitle := "renamed"
	newDesc := "new description"
	completed := models.TaskStatusCompleted
	inProgress := models.TaskStatusInProgress
	high := models.TaskPriorityHigh
	due := fixedNow.Add(72 * time.Hour)

	t.Run("updates every provided field", func(t *testing.T) {
		s := newTestService()
		addTask(s, "t1", models.TaskStatusPending, nil)
		task, err := s.UpdateTask("t1", models.UpdateTaskRequest{
			Title: &newTitle, Description: &newDesc, Status: &inProgress, Priority: &high,
			Tags: []string{"x"}, AssignedTo: strPtr("a@b.com"), DueDate: &due,
		})
		if err != nil {
			t.Fatal(err)
		}
		if task.Title != newTitle || task.Description != newDesc || task.Status != inProgress ||
			task.Priority != high || len(task.Tags) != 1 || task.AssignedTo == nil || *task.AssignedTo != "a@b.com" ||
			task.DueDate == nil || !task.DueDate.Equal(due) || task.CompletedAt != nil {
			t.Errorf("unexpected task: %+v", task)
		}
		if task.UpdatedAt.IsZero() {
			t.Error("UpdatedAt must be set")
		}
	})

	t.Run("empty request leaves fields unchanged", func(t *testing.T) {
		s := newTestService()
		addTask(s, "t1", models.TaskStatusPending, nil)
		task, err := s.UpdateTask("t1", models.UpdateTaskRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if task.Title != "t1" || task.Status != models.TaskStatusPending || task.Priority != models.TaskPriorityMedium {
			t.Errorf("unexpected change: %+v", task)
		}
	})

	t.Run("completing sets CompletedAt once", func(t *testing.T) {
		s := newTestService()
		addTask(s, "t1", models.TaskStatusPending, nil)
		task, err := s.UpdateTask("t1", models.UpdateTaskRequest{Status: &completed})
		if err != nil || task.CompletedAt == nil {
			t.Fatalf("CompletedAt not set: %+v, %v", task, err)
		}
		first := *task.CompletedAt
		time.Sleep(2 * time.Millisecond)
		task, err = s.UpdateTask("t1", models.UpdateTaskRequest{Status: &completed})
		if err != nil || !task.CompletedAt.Equal(first) {
			t.Errorf("CompletedAt changed on repeat completion")
		}
	})

	t.Run("invalid status", func(t *testing.T) {
		s := newTestService()
		addTask(s, "t1", models.TaskStatusPending, nil)
		bad := models.TaskStatus("bogus")
		_, err := s.UpdateTask("t1", models.UpdateTaskRequest{Status: &bad})
		if err == nil || !strings.Contains(err.Error(), "invalid status: bogus") {
			t.Errorf("err = %v", err)
		}
		if s.tasks["t1"].Status != models.TaskStatusPending {
			t.Error("status must not change on invalid input")
		}
	})

	t.Run("invalid priority", func(t *testing.T) {
		s := newTestService()
		addTask(s, "t1", models.TaskStatusPending, nil)
		bad := models.TaskPriority("urgent")
		_, err := s.UpdateTask("t1", models.UpdateTaskRequest{Priority: &bad})
		if err == nil || !strings.Contains(err.Error(), "invalid priority: urgent") {
			t.Errorf("err = %v", err)
		}
		if s.tasks["t1"].Priority != models.TaskPriorityMedium {
			t.Error("priority must not change on invalid input")
		}
	})

	// UpdateTask is not atomic: fields applied before a validation failure are
	// kept. These subtests pin that behaviour; flip them if UpdateTask is made
	// atomic.
	t.Run("invalid status after title keeps earlier mutation", func(t *testing.T) {
		s := newTestService()
		addTask(s, "t1", models.TaskStatusPending, nil)
		bad := models.TaskStatus("bogus")
		_, err := s.UpdateTask("t1", models.UpdateTaskRequest{Title: strPtr("x"), Status: &bad})
		if err == nil || !strings.Contains(err.Error(), "invalid status: bogus") {
			t.Fatalf("err = %v", err)
		}
		if got := s.tasks["t1"]; got.Title != "x" || got.Status != models.TaskStatusPending {
			t.Errorf("title=%q status=%q, want title persisted and status unchanged", got.Title, got.Status)
		}
	})

	t.Run("invalid priority after description keeps earlier mutation", func(t *testing.T) {
		s := newTestService()
		addTask(s, "t1", models.TaskStatusPending, nil)
		bad := models.TaskPriority("urgent")
		_, err := s.UpdateTask("t1", models.UpdateTaskRequest{Description: strPtr("d"), Priority: &bad})
		if err == nil || !strings.Contains(err.Error(), "invalid priority: urgent") {
			t.Fatalf("err = %v", err)
		}
		if got := s.tasks["t1"]; got.Description != "d" || got.Priority != models.TaskPriorityMedium {
			t.Errorf("description=%q priority=%q", got.Description, got.Priority)
		}
	})

	t.Run("not found", func(t *testing.T) {
		s := newTestService()
		_, err := s.UpdateTask("nope", models.UpdateTaskRequest{Title: &newTitle})
		if err == nil || err.Error() != "task with ID nope not found" {
			t.Errorf("err = %v", err)
		}
	})
}

func TestUpdateTaskStatus(t *testing.T) {
	s := newTestService()
	addTask(s, "t1", models.TaskStatusPending, nil)

	task, err := s.UpdateTaskStatus("t1", models.TaskStatusOnHold)
	if err != nil || task.Status != models.TaskStatusOnHold {
		t.Fatalf("got %+v, %v", task, err)
	}
	task, err = s.UpdateTaskStatus("t1", models.TaskStatusCompleted)
	if err != nil || task.CompletedAt == nil {
		t.Fatalf("completion: %+v, %v", task, err)
	}
	if _, err := s.UpdateTaskStatus("t1", "bogus"); err == nil {
		t.Error("expected error for invalid status")
	}
	if _, err := s.UpdateTaskStatus("missing", models.TaskStatusPending); err == nil {
		t.Error("expected not-found error")
	}
}

func TestDeleteTask(t *testing.T) {
	s := newTestService()
	addTask(s, "t1", models.TaskStatusPending, nil)

	if err := s.DeleteTask("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetTask("t1"); err == nil {
		t.Error("task still present after delete")
	}
	if err := s.DeleteTask("t1"); err == nil || err.Error() != "task with ID t1 not found" {
		t.Errorf("second delete err = %v", err)
	}
}

func TestGetStats(t *testing.T) {
	t.Run("empty service has zeroed stable keys", func(t *testing.T) {
		stats := newTestService().GetStats()
		if stats.Total != 0 || len(stats.ByStatus) != 5 || len(stats.ByPriority) != 4 {
			t.Fatalf("unexpected stats: %+v", stats)
		}
		for k, v := range stats.ByStatus {
			if v != 0 {
				t.Errorf("status %s = %d", k, v)
			}
		}
	})

	t.Run("counts by status and priority", func(t *testing.T) {
		s := newTestService()
		seed(s, models.Task{ID: "1", Status: models.TaskStatusPending, Priority: models.TaskPriorityLow})
		seed(s, models.Task{ID: "2", Status: models.TaskStatusPending, Priority: models.TaskPriorityHigh})
		seed(s, models.Task{ID: "3", Status: models.TaskStatusCompleted, Priority: models.TaskPriorityHigh})
		seed(s, models.Task{ID: "4", Status: models.TaskStatusOnHold, Priority: models.TaskPriorityCritical})
		stats := s.GetStats()
		if stats.Total != 4 || stats.ByStatus[models.TaskStatusPending] != 2 ||
			stats.ByStatus[models.TaskStatusCompleted] != 1 || stats.ByStatus[models.TaskStatusOnHold] != 1 ||
			stats.ByStatus[models.TaskStatusCancelled] != 0 ||
			stats.ByPriority[models.TaskPriorityHigh] != 2 || stats.ByPriority[models.TaskPriorityLow] != 1 ||
			stats.ByPriority[models.TaskPriorityCritical] != 1 || stats.ByPriority[models.TaskPriorityMedium] != 0 {
			t.Errorf("unexpected stats: %+v", stats)
		}
	})
}

func TestSearchTasks(t *testing.T) {
	s := newTestService()
	seed(s, models.Task{ID: "title", Title: "Deploy Server", Description: "x", Tags: []string{"ops"}})
	seed(s, models.Task{ID: "desc", Title: "Other", Description: "deploy notes", Tags: []string{"ops"}})
	seed(s, models.Task{ID: "tag", Title: "Misc", Description: "none", Tags: []string{"Deploy-tag"}})
	seed(s, models.Task{ID: "none", Title: "Nothing", Description: "here", Tags: []string{"zzz"}})
	seed(s, models.Task{ID: "all", Title: "deploy", Description: "deploy", Tags: []string{"deploy", "deploy2"}})

	t.Run("relevance order and case insensitivity", func(t *testing.T) {
		resp := s.SearchTasks(models.SearchTasksQuery{Query: "DEPLOY", PageSize: 20})
		got := ids(resp.Tasks)
		if resp.TotalCount != 4 || resp.Query != "DEPLOY" || len(got) != 4 {
			t.Fatalf("resp = %+v", resp)
		}
		// scores: all=6, title=3, desc=2, tag=1 (distinct, so order is deterministic)
		if !equal(got, []string{"all", "title", "desc", "tag"}) {
			t.Errorf("order = %v", got)
		}
	})

	t.Run("limit keeps total count", func(t *testing.T) {
		resp := s.SearchTasks(models.SearchTasksQuery{Query: "deploy", PageSize: 2})
		if len(resp.Tasks) != 2 || resp.TotalCount != 4 {
			t.Errorf("len=%d total=%d", len(resp.Tasks), resp.TotalCount)
		}
	})

	t.Run("non-positive page size falls back to 20", func(t *testing.T) {
		resp := s.SearchTasks(models.SearchTasksQuery{Query: "deploy"})
		if len(resp.Tasks) != 4 {
			t.Errorf("len = %d", len(resp.Tasks))
		}
	})

	t.Run("no matches returns empty non-nil slice", func(t *testing.T) {
		resp := s.SearchTasks(models.SearchTasksQuery{Query: "absent", PageSize: 5})
		if resp.Tasks == nil || len(resp.Tasks) != 0 || resp.TotalCount != 0 {
			t.Errorf("resp = %+v", resp)
		}
	})

	t.Run("matches tag only", func(t *testing.T) {
		resp := s.SearchTasks(models.SearchTasksQuery{Query: "zzz", PageSize: 5})
		if !equal(ids(resp.Tasks), []string{"none"}) {
			t.Errorf("got %v", ids(resp.Tasks))
		}
	})
}

func TestListTasksFilters(t *testing.T) {
	s := newTestService()
	seed(s, models.Task{ID: "1", Status: models.TaskStatusPending, AssignedTo: strPtr("a@x.com"), Tags: []string{"go", "api"}})
	seed(s, models.Task{ID: "2", Status: models.TaskStatusCompleted, AssignedTo: strPtr("b@x.com"), Tags: []string{"go"}})
	seed(s, models.Task{ID: "3", Status: models.TaskStatusPending, Tags: []string{"api", "docs"}})
	seed(s, models.Task{ID: "4", Status: models.TaskStatusPending, Tags: nil})

	tests := []struct {
		name  string
		query models.ListTasksQuery
		want  []string // compared as a set via sorted ids
	}{
		{"no filter", models.ListTasksQuery{PageSize: 100}, []string{"1", "2", "3", "4"}},
		{"status", models.ListTasksQuery{PageSize: 100, Status: models.TaskStatusPending}, []string{"1", "3", "4"}},
		{"unknown status matches nothing", models.ListTasksQuery{PageSize: 100, Status: "bogus"}, nil},
		{"assignee", models.ListTasksQuery{PageSize: 100, AssignedTo: "a@x.com"}, []string{"1"}},
		{"assignee unknown", models.ListTasksQuery{PageSize: 100, AssignedTo: "z@x.com"}, nil},
		{"single tag", models.ListTasksQuery{PageSize: 100, Tags: "go"}, []string{"1", "2"}},
		{"all tags required", models.ListTasksQuery{PageSize: 100, Tags: "go,api"}, []string{"1"}},
		{"tags are trimmed", models.ListTasksQuery{PageSize: 100, Tags: "api, docs"}, []string{"3"}},
		{"missing tag", models.ListTasksQuery{PageSize: 100, Tags: "go,nope"}, nil},
		{"combined", models.ListTasksQuery{PageSize: 100, Status: models.TaskStatusPending, Tags: "api"}, []string{"1", "3"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.ListTasks(tt.query)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]bool{}
			for _, id := range ids(resp.Tasks) {
				got[id] = true
			}
			if len(got) != len(tt.want) || resp.TotalCount != len(tt.want) {
				t.Fatalf("got %v (total %d), want %v", ids(resp.Tasks), resp.TotalCount, tt.want)
			}
			for _, id := range tt.want {
				if !got[id] {
					t.Errorf("missing %s in %v", id, ids(resp.Tasks))
				}
			}
		})
	}
}

func TestSortTasks(t *testing.T) {
	base := fixedNow
	s := newTestService()
	seed(s, models.Task{ID: "a", CreatedAt: base.Add(2 * time.Hour), DueDate: at(3 * time.Hour), Priority: models.TaskPriorityLow})
	seed(s, models.Task{ID: "b", CreatedAt: base.Add(1 * time.Hour), DueDate: at(1 * time.Hour), Priority: models.TaskPriorityCritical})
	seed(s, models.Task{ID: "c", CreatedAt: base.Add(3 * time.Hour), DueDate: nil, Priority: models.TaskPriorityHigh})
	seed(s, models.Task{ID: "d", CreatedAt: base.Add(4 * time.Hour), DueDate: at(2 * time.Hour), Priority: models.TaskPriorityMedium})

	tests := []struct {
		order models.SortOrder
		want  []string
	}{
		{models.SortOrderCreatedAtAsc, []string{"b", "a", "c", "d"}},
		{models.SortOrderCreatedAtDesc, []string{"d", "c", "a", "b"}},
		{models.SortOrderPriorityAsc, []string{"a", "d", "c", "b"}},
		{models.SortOrderPriorityDesc, []string{"b", "c", "d", "a"}},
	}
	for _, tt := range tests {
		t.Run(string(tt.order), func(t *testing.T) {
			resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 100, SortOrder: tt.order})
			if got := ids(resp.Tasks); !equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}

	// Due-date ordering: tasks without a due date are expected after dated ones.
	t.Run("due_date_asc", func(t *testing.T) {
		resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 100, SortOrder: models.SortOrderDueDateAsc})
		got := ids(resp.Tasks)
		if !equal(got[:3], []string{"b", "d", "a"}) || got[3] != "c" {
			t.Errorf("got %v", got)
		}
	})
	t.Run("due_date_desc", func(t *testing.T) {
		resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 100, SortOrder: models.SortOrderDueDateDesc})
		got := ids(resp.Tasks)
		if !equal(got[:3], []string{"a", "d", "b"}) || got[3] != "c" {
			t.Errorf("got %v", got)
		}
	})
}

func TestSortTasksUnknownOrderKeepsAllTasks(t *testing.T) {
	s := newTestService()
	addTask(s, "1", models.TaskStatusPending, nil)
	addTask(s, "2", models.TaskStatusPending, nil)
	resp, _ := s.ListTasks(models.ListTasksQuery{PageSize: 10, SortOrder: "weird"})
	if len(resp.Tasks) != 2 {
		t.Errorf("len = %d", len(resp.Tasks))
	}
}

func TestListTasksPagination(t *testing.T) {
	s := newTestService()
	base := fixedNow
	for i := 0; i < 5; i++ {
		seed(s, models.Task{ID: fmt.Sprintf("t%d", i), CreatedAt: base.Add(time.Duration(i) * time.Minute)})
	}
	q := func(size int, token string) *models.ListTasksResponse {
		resp, err := s.ListTasks(models.ListTasksQuery{PageSize: size, PageToken: token, SortOrder: models.SortOrderCreatedAtAsc})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	tests := []struct {
		name  string
		size  int
		token string
		want  []string
		next  string
	}{
		{"first page", 2, "", []string{"t0", "t1"}, "2"},
		{"second page", 2, "2", []string{"t2", "t3"}, "4"},
		{"last partial page", 2, "4", []string{"t4"}, ""},
		{"exact fit has no next token", 5, "", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"zero page size defaults to 20", 0, "", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"negative page size defaults to 20", -3, "", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"oversized page size is capped", 1000, "", []string{"t0", "t1", "t2", "t3", "t4"}, ""},
		{"token past end", 2, "99", nil, ""},
		{"token at end", 2, "5", nil, ""},
		{"non-numeric token starts at zero", 2, "abc", []string{"t0", "t1"}, "2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := q(tt.size, tt.token)
			if got := ids(resp.Tasks); !equal(got, tt.want) {
				t.Errorf("tasks = %v, want %v", got, tt.want)
			}
			if resp.NextPageToken != tt.next {
				t.Errorf("next = %q, want %q", resp.NextPageToken, tt.next)
			}
			if resp.TotalCount != 5 {
				t.Errorf("total = %d, want 5", resp.TotalCount)
			}
		})
	}
}

func TestPaginateCapsAt100(t *testing.T) {
	var tasks []*models.Task
	for i := 0; i < 150; i++ {
		tasks = append(tasks, &models.Task{ID: fmt.Sprint(i)})
	}
	page, next := paginate(tasks, 500, "")
	if len(page) != 100 || next != "100" {
		t.Errorf("len=%d next=%q", len(page), next)
	}
	page, next = paginate(tasks, 100, "100")
	if len(page) != 50 || next != "" {
		t.Errorf("len=%d next=%q", len(page), next)
	}
}

func TestFilterTasksAndSortTasksHelpers(t *testing.T) {
	s := newTestService()
	a := &models.Task{ID: "a", Status: models.TaskStatusPending, Priority: models.TaskPriorityHigh}
	b := &models.Task{ID: "b", Status: models.TaskStatusCompleted, Priority: models.TaskPriorityLow}
	got := s.filterTasks([]*models.Task{a, b}, models.ListTasksQuery{Status: models.TaskStatusCompleted})
	if len(got) != 1 || got[0].ID != "b" {
		t.Errorf("filterTasks = %v", got)
	}
	if got := s.filterTasks(nil, models.ListTasksQuery{}); len(got) != 0 {
		t.Errorf("filterTasks(nil) = %v", got)
	}
	list := []*models.Task{b, a}
	s.sortTasks(list, models.SortOrderPriorityDesc)
	if list[0].ID != "a" {
		t.Errorf("sortTasks order = %s,%s", list[0].ID, list[1].ID)
	}
	s.sortTasks(nil, models.SortOrderCreatedAtAsc) // must not panic
}

func TestConcurrentAccess(t *testing.T) {
	s := newTestService()
	var wg sync.WaitGroup
	const workers = 20
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			task, err := s.CreateTask(models.CreateTaskRequest{Title: fmt.Sprintf("task-%d", i)})
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			_, _ = s.ListTasks(models.ListTasksQuery{PageSize: 10})
			_ = s.GetStats()
			_ = s.SearchTasks(models.SearchTasksQuery{Query: "task", PageSize: 5})
			_ = s.ListOverdueTasks(models.OverdueTasksQuery{PageSize: 5})
			if _, err := s.UpdateTaskStatus(task.ID, models.TaskStatusInProgress); err != nil {
				t.Errorf("update: %v", err)
			}
			if _, err := s.AssignTask(task.ID, "a@b.com"); err != nil {
				t.Errorf("assign: %v", err)
			}
			if i%2 == 0 {
				if err := s.DeleteTask(task.ID); err != nil {
					t.Errorf("delete: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if got := s.GetStats().Total; got != workers/2 {
		t.Errorf("total = %d, want %d", got, workers/2)
	}
}
