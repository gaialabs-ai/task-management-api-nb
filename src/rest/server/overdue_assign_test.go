package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/services"
)

var httpNow = time.Date(2024, 6, 1, 12, 0, 0, 0, time.UTC)

type recordingNotifier struct {
	mu    sync.Mutex
	tos   []string
	ids   []string
	fails bool
}

func (n *recordingNotifier) NotifyAssignment(task models.Task, to string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.tos = append(n.tos, to)
	n.ids = append(n.ids, task.ID)
	if n.fails {
		return errors.New("boom")
	}
	return nil
}

func newOverdueFixture(t *testing.T, n services.Notifier) (*services.TaskService, http.Handler) {
	t.Helper()
	opts := []services.Option{services.WithClock(func() time.Time { return httpNow })}
	if n != nil {
		opts = append(opts, services.WithNotifier(n))
	}
	svc := services.NewTaskService(opts...)
	return svc, newTestRouter(svc)
}

func createTask(t *testing.T, r http.Handler, body string) models.Task {
	t.Helper()
	w := doRequest(r, "POST", "/api/v1/tasks", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	var task models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &task); err != nil {
		t.Fatal(err)
	}
	return task
}

func TestOverdueRoute_NotCapturedByID(t *testing.T) {
	_, r := newOverdueFixture(t, nil)
	w := doRequest(r, "GET", "/api/v1/tasks/overdue", "")
	if w.Code != http.StatusOK {
		t.Fatalf("got %d %s", w.Code, w.Body.String())
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["total_count"]; !ok {
		t.Fatalf("response is not a list response (captured by /:id?): %s", w.Body.String())
	}
	if _, ok := raw["error"]; ok {
		t.Fatalf("unexpected error: %s", w.Body.String())
	}
}

func TestOverdueHTTP_FilterOrderPaginate(t *testing.T) {
	_, r := newOverdueFixture(t, nil)
	day := func(d int) string { return httpNow.Add(time.Duration(d) * 24 * time.Hour).Format(time.RFC3339) }
	createTask(t, r, fmt.Sprintf(`{"title":"late-2d","due_date":%q}`, day(-2)))
	createTask(t, r, fmt.Sprintf(`{"title":"late-5d","due_date":%q}`, day(-5)))
	createTask(t, r, fmt.Sprintf(`{"title":"late-1d","due_date":%q}`, day(-1)))
	createTask(t, r, fmt.Sprintf(`{"title":"future","due_date":%q}`, day(3)))
	createTask(t, r, `{"title":"nodue"}`)
	done := createTask(t, r, fmt.Sprintf(`{"title":"done","due_date":%q}`, day(-9)))
	if w := doRequest(r, "PATCH", "/api/v1/tasks/"+done.ID+"/status", `{"status":"completed"}`); w.Code != 200 {
		t.Fatal(w.Body.String())
	}

	resp := decodeList(t, doRequest(r, "GET", "/api/v1/tasks/overdue", ""))
	if resp.TotalCount != 3 || len(resp.Tasks) != 3 || resp.NextPageToken != "" {
		t.Fatalf("%+v", resp)
	}
	want := []string{"late-5d", "late-2d", "late-1d"}
	for i, tk := range resp.Tasks {
		if tk.Title != want[i] {
			t.Fatalf("order: pos %d got %q want %q", i, tk.Title, want[i])
		}
	}

	resp = decodeList(t, doRequest(r, "GET", "/api/v1/tasks/overdue?page_size=2", ""))
	if len(resp.Tasks) != 2 || resp.NextPageToken != "2" || resp.TotalCount != 3 {
		t.Fatalf("page1 %+v", resp)
	}
	resp = decodeList(t, doRequest(r, "GET", "/api/v1/tasks/overdue?page_size=2&page_token=2", ""))
	if len(resp.Tasks) != 1 || resp.Tasks[0].Title != "late-1d" || resp.NextPageToken != "" {
		t.Fatalf("page2 %+v", resp)
	}
}

func TestOverdueHTTP_BadPageSize(t *testing.T) {
	_, r := newOverdueFixture(t, nil)
	for _, ps := range []string{"0", "-1", "101", "abc"} {
		w := doRequest(r, "GET", "/api/v1/tasks/overdue?page_size="+ps, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("page_size=%s got %d", ps, w.Code)
			continue
		}
		if e := decodeErr(t, w); e.Error.Code != "VALIDATION_ERROR" {
			t.Errorf("page_size=%s code %q", ps, e.Error.Code)
		}
	}
}

func TestAssignHTTP_Success(t *testing.T) {
	n := &recordingNotifier{}
	_, r := newOverdueFixture(t, n)
	task := createTask(t, r, `{"title":"x"}`)

	w := doRequest(r, "POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var got models.Task
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != task.ID || got.AssignedTo == nil || *got.AssignedTo != "bob@example.com" || !got.UpdatedAt.Equal(httpNow) {
		t.Fatalf("%+v", got)
	}
	if len(n.tos) != 1 || n.tos[0] != "bob@example.com" || n.ids[0] != task.ID {
		t.Fatalf("notifier %+v", n)
	}
	// Persisted
	w = doRequest(r, "GET", "/api/v1/tasks/"+task.ID, "")
	var fetched models.Task
	json.Unmarshal(w.Body.Bytes(), &fetched)
	if fetched.AssignedTo == nil || *fetched.AssignedTo != "bob@example.com" {
		t.Fatalf("not persisted: %s", w.Body.String())
	}
}

func TestAssignHTTP_NotifierFailureStillSucceeds(t *testing.T) {
	n := &recordingNotifier{fails: true}
	_, r := newOverdueFixture(t, n)
	task := createTask(t, r, `{"title":"x"}`)
	w := doRequest(r, "POST", "/api/v1/tasks/"+task.ID+"/assign", `{"assigned_to":"bob@example.com"}`)
	if w.Code != http.StatusOK || len(n.tos) != 1 {
		t.Fatalf("%d %s calls=%d", w.Code, w.Body.String(), len(n.tos))
	}
}

func TestAssignHTTP_Errors(t *testing.T) {
	n := &recordingNotifier{}
	_, r := newOverdueFixture(t, n)
	task := createTask(t, r, `{"title":"x"}`)
	url := "/api/v1/tasks/" + task.ID + "/assign"

	cases := []struct {
		name, url, body string
		status          int
		code            string
	}{
		{"invalid email", url, `{"assigned_to":"not-an-email"}`, 400, "VALIDATION_ERROR"},
		{"display name", url, `{"assigned_to":"Bob <bob@example.com>"}`, 400, "VALIDATION_ERROR"},
		{"missing body", url, ``, 400, "VALIDATION_ERROR"},
		{"empty object", url, `{}`, 400, "VALIDATION_ERROR"},
		{"empty email", url, `{"assigned_to":""}`, 400, "VALIDATION_ERROR"},
		{"malformed json", url, `{`, 400, "VALIDATION_ERROR"},
		{"unknown task", "/api/v1/tasks/nope/assign", `{"assigned_to":"bob@example.com"}`, 404, "NOT_FOUND"},
	}
	for _, c := range cases {
		w := doRequest(r, "POST", c.url, c.body)
		if w.Code != c.status {
			t.Errorf("%s: got %d %s", c.name, w.Code, w.Body.String())
			continue
		}
		e := decodeErr(t, w)
		if e.Error.Code != c.code || e.Error.Message == "" {
			t.Errorf("%s: error %+v", c.name, e)
		}
	}
	if len(n.tos) != 0 {
		t.Fatalf("notifier called on failures: %v", n.tos)
	}
}

func TestNewHandler_StillCompilesWithDefaultService(t *testing.T) {
	r := newTestRouter(services.NewTaskService())
	if w := doRequest(r, "GET", "/health", ""); w.Code != http.StatusOK {
		t.Fatalf("health %d", w.Code)
	}
}
