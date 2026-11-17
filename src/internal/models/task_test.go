package models

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestTaskStatusIsValid(t *testing.T) {
	tests := []struct {
		status TaskStatus
		want   bool
	}{
		{TaskStatusPending, true},
		{TaskStatusInProgress, true},
		{TaskStatusCompleted, true},
		{TaskStatusCancelled, true},
		{TaskStatusOnHold, true},
		{"", false},
		{"PENDING", false},
		{"done", false},
		{"in progress", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.status), func(t *testing.T) {
			if got := tt.status.IsValid(); got != tt.want {
				t.Errorf("TaskStatus(%q).IsValid() = %v, want %v", tt.status, got, tt.want)
			}
		})
	}
}

func TestTaskPriorityIsValid(t *testing.T) {
	tests := []struct {
		priority TaskPriority
		want     bool
	}{
		{TaskPriorityLow, true},
		{TaskPriorityMedium, true},
		{TaskPriorityHigh, true},
		{TaskPriorityCritical, true},
		{"", false},
		{"HIGH", false},
		{"urgent", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.priority), func(t *testing.T) {
			if got := tt.priority.IsValid(); got != tt.want {
				t.Errorf("TaskPriority(%q).IsValid() = %v, want %v", tt.priority, got, tt.want)
			}
		})
	}
}

func TestTaskPriorityGetValue(t *testing.T) {
	tests := []struct {
		priority TaskPriority
		want     int
	}{
		{TaskPriorityLow, 1},
		{TaskPriorityMedium, 2},
		{TaskPriorityHigh, 3},
		{TaskPriorityCritical, 4},
		{"", 2},
		{"unknown", 2},
	}
	for _, tt := range tests {
		t.Run(string(tt.priority), func(t *testing.T) {
			if got := tt.priority.GetValue(); got != tt.want {
				t.Errorf("TaskPriority(%q).GetValue() = %d, want %d", tt.priority, got, tt.want)
			}
		})
	}
}

func TestTaskPriorityOrdering(t *testing.T) {
	order := []TaskPriority{TaskPriorityLow, TaskPriorityMedium, TaskPriorityHigh, TaskPriorityCritical}
	for i := 1; i < len(order); i++ {
		if order[i-1].GetValue() >= order[i].GetValue() {
			t.Errorf("%s should rank below %s", order[i-1], order[i])
		}
	}
}

func TestTaskJSONRoundTrip(t *testing.T) {
	created := time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)
	due := created.Add(24 * time.Hour)
	assignee := "bob@example.com"
	task := Task{
		ID:          "t1",
		Title:       "Title",
		Description: "Desc",
		Status:      TaskStatusInProgress,
		Priority:    TaskPriorityHigh,
		Tags:        []string{"a", "b"},
		CreatedBy:   "system",
		AssignedTo:  &assignee,
		CreatedAt:   created,
		UpdatedAt:   created,
		DueDate:     &due,
	}

	data, err := json.Marshal(task)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"id", "title", "description", "status", "priority", "tags", "created_by", "assigned_to", "created_at", "updated_at", "due_date"} {
		if _, ok := raw[key]; !ok {
			t.Errorf("missing JSON key %q in %s", key, data)
		}
	}
	if _, ok := raw["completed_at"]; ok {
		t.Errorf("completed_at should be omitted when nil: %s", data)
	}

	var back Task
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if back.ID != task.ID || back.Status != task.Status || back.Priority != task.Priority ||
		back.AssignedTo == nil || *back.AssignedTo != assignee ||
		back.DueDate == nil || !back.DueDate.Equal(due) || !back.CreatedAt.Equal(created) ||
		len(back.Tags) != 2 || back.CompletedAt != nil {
		t.Errorf("round trip mismatch: %+v", back)
	}
}

func TestTaskJSONOmitsOptionalFields(t *testing.T) {
	data, err := json.Marshal(Task{ID: "t1", Tags: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"assigned_to", "due_date", "completed_at"} {
		if strings.Contains(string(data), `"`+key+`"`) {
			t.Errorf("%s should be omitted: %s", key, data)
		}
	}
	if !strings.Contains(string(data), `"tags":[]`) {
		t.Errorf("empty tags should serialize as []: %s", data)
	}
}

func TestCreateTaskRequestJSON(t *testing.T) {
	body := `{"title":"T","description":"D","priority":"high","tags":["x"],"assigned_to":"a@b.com","due_date":"2024-06-02T10:00:00Z"}`
	var req CreateTaskRequest
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		t.Fatal(err)
	}
	if req.Title != "T" || req.Description != "D" || req.Priority != TaskPriorityHigh || len(req.Tags) != 1 ||
		req.AssignedTo == nil || *req.AssignedTo != "a@b.com" ||
		req.DueDate == nil || !req.DueDate.Equal(time.Date(2024, 6, 2, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("unexpected decode: %+v", req)
	}

	var minimal CreateTaskRequest
	if err := json.Unmarshal([]byte(`{"title":"only"}`), &minimal); err != nil {
		t.Fatal(err)
	}
	if minimal.Priority != "" || minimal.Tags != nil || minimal.AssignedTo != nil || minimal.DueDate != nil {
		t.Errorf("optional fields should be zero: %+v", minimal)
	}

	if err := json.Unmarshal([]byte(`{"title":"x","due_date":"not-a-date"}`), &minimal); err == nil {
		t.Error("expected error for invalid due_date")
	}
}

func TestUpdateTaskRequestJSON(t *testing.T) {
	var req UpdateTaskRequest
	if err := json.Unmarshal([]byte(`{"title":"n","status":"completed","priority":"low"}`), &req); err != nil {
		t.Fatal(err)
	}
	if req.Title == nil || *req.Title != "n" || req.Status == nil || *req.Status != TaskStatusCompleted ||
		req.Priority == nil || *req.Priority != TaskPriorityLow {
		t.Errorf("unexpected decode: %+v", req)
	}
	if req.Description != nil || req.Tags != nil || req.AssignedTo != nil || req.DueDate != nil {
		t.Errorf("absent fields must stay nil: %+v", req)
	}

	var empty UpdateTaskRequest
	if err := json.Unmarshal([]byte(`{}`), &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Title != nil || empty.Status != nil {
		t.Errorf("empty body must leave all fields nil: %+v", empty)
	}
}

func TestSimpleRequestJSON(t *testing.T) {
	var status UpdateTaskStatusRequest
	if err := json.Unmarshal([]byte(`{"status":"on_hold"}`), &status); err != nil || status.Status != TaskStatusOnHold {
		t.Errorf("status decode: %+v err=%v", status, err)
	}
	var assign AssignTaskRequest
	if err := json.Unmarshal([]byte(`{"email":"a@b.com"}`), &assign); err != nil || assign.Email != "a@b.com" {
		t.Errorf("assign decode: %+v err=%v", assign, err)
	}
	var comment CreateCommentRequest
	if err := json.Unmarshal([]byte(`{"author":"al","body":"hi"}`), &comment); err != nil || comment.Author != "al" || comment.Body != "hi" {
		t.Errorf("comment decode: %+v err=%v", comment, err)
	}
}

func TestResponseJSON(t *testing.T) {
	list, err := json.Marshal(ListTasksResponse{Tasks: []Task{}, TotalCount: 0})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(list), "next_page_token") {
		t.Errorf("empty next_page_token should be omitted: %s", list)
	}
	if !strings.Contains(string(list), `"total_count":0`) || !strings.Contains(string(list), `"tasks":[]`) {
		t.Errorf("unexpected list JSON: %s", list)
	}

	withToken, _ := json.Marshal(ListTasksResponse{NextPageToken: "20"})
	if !strings.Contains(string(withToken), `"next_page_token":"20"`) {
		t.Errorf("token missing: %s", withToken)
	}

	stats, _ := json.Marshal(TaskStats{
		Total:      3,
		ByStatus:   map[TaskStatus]int{TaskStatusPending: 3},
		ByPriority: map[TaskPriority]int{TaskPriorityLow: 3},
	})
	if !strings.Contains(string(stats), `"by_status":{"pending":3}`) || !strings.Contains(string(stats), `"by_priority":{"low":3}`) {
		t.Errorf("unexpected stats JSON: %s", stats)
	}

	search, _ := json.Marshal(SearchTasksResponse{Tasks: []Task{}, TotalCount: 1, Query: "q"})
	if !strings.Contains(string(search), `"query":"q"`) || !strings.Contains(string(search), `"total_count":1`) {
		t.Errorf("unexpected search JSON: %s", search)
	}

	cl, _ := json.Marshal(ListCommentsResponse{Comments: []Comment{{ID: "c", TaskID: "t", Author: "a", Body: "b"}}, Total: 1})
	for _, want := range []string{`"comments":[`, `"task_id":"t"`, `"total":1`, `"created_at"`, `"updated_at"`} {
		if !strings.Contains(string(cl), want) {
			t.Errorf("comments JSON missing %s: %s", want, cl)
		}
	}
}

func TestErrorResponseJSON(t *testing.T) {
	data, _ := json.Marshal(ErrorResponse{Error: ErrorDetail{Code: "NOT_FOUND", Message: "nope"}})
	if string(data) != `{"error":{"code":"NOT_FOUND","message":"nope"}}` {
		t.Errorf("unexpected error JSON: %s", data)
	}
	data, _ = json.Marshal(ErrorResponse{Error: ErrorDetail{Code: "C", Message: "M", Details: map[string]string{"k": "v"}}})
	if !strings.Contains(string(data), `"details":{"k":"v"}`) {
		t.Errorf("details missing: %s", data)
	}
}
