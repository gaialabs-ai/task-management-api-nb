package services

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cloudstreet-dev/REST-gRPC-in-33-Languages/code/go/internal/models"
)

func newCommentFixture() (*CommentService, *TaskService) {
	ts := newTestService()
	addTask(ts, "t1", models.TaskStatusPending, nil)
	addTask(ts, "t2", models.TaskStatusPending, nil)
	return NewCommentService(ts), ts
}

func TestListCommentsEmpty(t *testing.T) {
	cs, _ := newCommentFixture()
	resp, err := cs.ListComments("t1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Comments == nil || len(resp.Comments) != 0 || resp.Total != 0 {
		t.Errorf("resp = %+v", resp)
	}
}

func TestListCommentsUnknownTask(t *testing.T) {
	cs, _ := newCommentFixture()
	resp, err := cs.ListComments("missing")
	if resp != nil || err == nil || err.Error() != "task with ID missing not found" {
		t.Errorf("resp=%v err=%v", resp, err)
	}
}

func TestAddComment(t *testing.T) {
	cs, _ := newCommentFixture()
	c, err := cs.AddComment("t1", models.CreateCommentRequest{Author: "alice", Body: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.TaskID != "t1" || c.Author != "alice" || c.Body != "hello" ||
		c.CreatedAt.IsZero() || !c.CreatedAt.Equal(c.UpdatedAt) {
		t.Errorf("unexpected comment: %+v", c)
	}

	resp, _ := cs.ListComments("t1")
	if resp.Total != 1 || resp.Comments[0].ID != c.ID {
		t.Errorf("list = %+v", resp)
	}
	// Comments are scoped per task.
	other, _ := cs.ListComments("t2")
	if other.Total != 0 {
		t.Errorf("t2 should have no comments: %+v", other)
	}
}

func TestAddCommentUnknownTask(t *testing.T) {
	cs, _ := newCommentFixture()
	c, err := cs.AddComment("missing", models.CreateCommentRequest{Author: "a", Body: "b"})
	if c != nil || err == nil || !strings.Contains(err.Error(), "task with ID missing not found") {
		t.Errorf("c=%v err=%v", c, err)
	}
	if len(cs.comments) != 0 {
		t.Error("no comment bucket should be created for unknown task")
	}
}

func TestAddCommentUniqueIDs(t *testing.T) {
	cs, _ := newCommentFixture()
	a, _ := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "1"})
	b, _ := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "2"})
	if a.ID == b.ID {
		t.Error("comment IDs must be unique")
	}
}

func TestListCommentsSortedByCreation(t *testing.T) {
	cs, _ := newCommentFixture()
	base := fixedNow
	// Insert out of order with fixed timestamps.
	cs.comments["t1"] = []*models.Comment{
		{ID: "late", TaskID: "t1", CreatedAt: base.Add(2 * time.Hour)},
		{ID: "early", TaskID: "t1", CreatedAt: base},
		{ID: "mid", TaskID: "t1", CreatedAt: base.Add(time.Hour)},
	}
	resp, err := cs.ListComments("t1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range resp.Comments {
		got = append(got, c.ID)
	}
	if !equal(got, []string{"early", "mid", "late"}) || resp.Total != 3 {
		t.Errorf("order = %v total=%d", got, resp.Total)
	}
}

func TestListCommentsReturnsCopies(t *testing.T) {
	cs, _ := newCommentFixture()
	c, _ := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "orig"})
	resp, _ := cs.ListComments("t1")
	resp.Comments[0].Body = "mutated"
	again, _ := cs.ListComments("t1")
	if again.Comments[0].Body != "orig" || again.Comments[0].ID != c.ID {
		t.Errorf("stored comment was mutated through list result: %+v", again.Comments[0])
	}
}

func TestDeleteComment(t *testing.T) {
	cs, _ := newCommentFixture()
	a, _ := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "1"})
	b, _ := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "2"})
	c, _ := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "3"})

	if err := cs.DeleteComment("t1", b.ID); err != nil {
		t.Fatal(err)
	}
	resp, _ := cs.ListComments("t1")
	remaining := map[string]bool{}
	for _, x := range resp.Comments {
		remaining[x.ID] = true
	}
	if resp.Total != 2 || !remaining[a.ID] || !remaining[c.ID] || remaining[b.ID] {
		t.Errorf("remaining = %v", remaining)
	}

	// Deleting the same comment again fails.
	err := cs.DeleteComment("t1", b.ID)
	if err == nil || !strings.Contains(err.Error(), "comment with ID "+b.ID+" not found on task t1") {
		t.Errorf("err = %v", err)
	}
}

func TestDeleteCommentErrors(t *testing.T) {
	cs, _ := newCommentFixture()
	c, _ := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "1"})

	if err := cs.DeleteComment("missing", c.ID); err == nil || !strings.Contains(err.Error(), "task with ID missing not found") {
		t.Errorf("unknown task err = %v", err)
	}
	if err := cs.DeleteComment("t1", "nope"); err == nil || !strings.Contains(err.Error(), "comment with ID nope not found") {
		t.Errorf("unknown comment err = %v", err)
	}
	// A comment on another task cannot be deleted through t2.
	if err := cs.DeleteComment("t2", c.ID); err == nil {
		t.Error("comment must not be deletable via a different task")
	}
	if resp, _ := cs.ListComments("t1"); resp.Total != 1 {
		t.Errorf("comment should still exist: %+v", resp)
	}
}

func TestDeletedTaskCommentsUnreachable(t *testing.T) {
	cs, ts := newCommentFixture()
	_, _ = cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "1"})
	if err := ts.DeleteTask("t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.ListComments("t1"); err == nil {
		t.Error("listing comments of a deleted task must fail")
	}
}

func TestCommentServiceConcurrency(t *testing.T) {
	cs, _ := newCommentFixture()
	var wg sync.WaitGroup
	const n = 30
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := cs.AddComment("t1", models.CreateCommentRequest{Author: "a", Body: "b"})
			if err != nil {
				t.Errorf("add: %v", err)
				return
			}
			_, _ = cs.ListComments("t1")
			_ = cs.DeleteComment("t1", c.ID)
		}()
	}
	wg.Wait()
	if resp, _ := cs.ListComments("t1"); resp.Total != 0 {
		t.Errorf("total = %d, want 0", resp.Total)
	}
}
