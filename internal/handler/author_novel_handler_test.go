package handler

// NovelService.Create fires notifyNewNovelCreated in its own detached
// goroutine (deliberately — see its doc comment) so a slow push/inbox
// write never delays the create response. In these tests that
// goroutine can still be running when t.Cleanup closes the pool,
// logging a harmless "closed pool" error after the test's own
// assertions have already passed — not a real failure.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/middleware"
	"github.com/ImtiazChowdhuryWork/novelora_backend/internal/testsupport"
)

// newAuthorNovelRequest builds a request the way middleware.Authenticate
// would have left it: UserIDContextKey set, and PathValue set the way
// Go 1.22's http.ServeMux populates it from a "{id}" pattern — tests
// call handlers directly, no real router needed.
func newAuthorNovelRequest(t *testing.T, method, path, callerUserID string, body any) *http.Request {
	t.Helper()
	var bodyReader *bytes.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request body: %v", err)
		}
		bodyReader = bytes.NewReader(encoded)
	} else {
		bodyReader = bytes.NewReader(nil)
	}
	request := httptest.NewRequest(method, path, bodyReader)
	ctx := context.WithValue(request.Context(), middleware.UserIDContextKey, callerUserID)
	return request.WithContext(ctx)
}

func TestAuthorNovelHandler_Create_LocksEditorialFields(t *testing.T) {
	pool := testsupport.NewPool(t)
	services := testsupport.NewServices(t, pool)
	ctx := context.Background()

	authorID, penName := testsupport.NewTestAuthor(t, ctx, services, "novel_create_author_")

	handler := NewAuthorNovelHandler(services.Novel, services.AuditLogger, t.TempDir())

	requestBody := novelWriteRequest{
		Title:         "Editorial Lockout Test Novel",
		AuthorName:    penName,
		Synopsis:      "test",
		Status:        "ongoing",
		IsRecommended: true, // attempting to self-promote
		IsExclusive:   true,
		Rating:        ptrFloat64(9.9),
		ViewCount:     999999,
	}
	request := newAuthorNovelRequest(t, http.MethodPost, "/api/v1/author/novels", authorID, requestBody)
	recorder := httptest.NewRecorder()

	handler.Create(recorder, request)
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM novels WHERE title = $1", requestBody.Title)
	})

	if recorder.Code != http.StatusCreated {
		t.Fatalf("Create status = %d, body = %s", recorder.Code, recorder.Body.String())
	}

	var created novelResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if created.IsRecommended {
		t.Error("Create did not lock out is_recommended — a crafted request set it to true")
	}
	if created.IsExclusive {
		t.Error("Create did not lock out is_exclusive — a crafted request set it to true")
	}
	if created.Rating != nil {
		t.Errorf("Create did not lock out rating — got %v, want nil", *created.Rating)
	}
	if created.ViewCount != 0 {
		t.Errorf("Create did not lock out view_count — got %d, want 0", created.ViewCount)
	}
}

func TestAuthorNovelHandler_OwnershipIsolation(t *testing.T) {
	pool := testsupport.NewPool(t)
	services := testsupport.NewServices(t, pool)
	ctx := context.Background()

	ownerID, ownerPenName := testsupport.NewTestAuthor(t, ctx, services, "novel_owner_")
	otherID, _ := testsupport.NewTestAuthor(t, ctx, services, "novel_other_")

	handler := NewAuthorNovelHandler(services.Novel, services.AuditLogger, t.TempDir())

	createRequest := newAuthorNovelRequest(t, http.MethodPost, "/api/v1/author/novels", ownerID, novelWriteRequest{
		Title:      "Ownership Isolation Test Novel",
		AuthorName: ownerPenName,
		Status:     "ongoing",
	})
	createRecorder := httptest.NewRecorder()
	handler.Create(createRecorder, createRequest)
	if createRecorder.Code != http.StatusCreated {
		t.Fatalf("setup Create status = %d, body = %s", createRecorder.Code, createRecorder.Body.String())
	}
	var novel novelResponse
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &novel); err != nil {
		t.Fatalf("unmarshal setup response: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, "DELETE FROM novels WHERE id = $1", novel.ID)
	})

	// The owner can fetch their own novel.
	getOwnRequest := newAuthorNovelRequest(t, http.MethodGet, "/api/v1/author/novels/"+novel.ID, ownerID, nil)
	getOwnRequest.SetPathValue("id", novel.ID)
	getOwnRecorder := httptest.NewRecorder()
	handler.Get(getOwnRecorder, getOwnRequest)
	if getOwnRecorder.Code != http.StatusOK {
		t.Fatalf("owner Get status = %d, want 200, body = %s", getOwnRecorder.Code, getOwnRecorder.Body.String())
	}

	// A different author gets a 404, not a 403 — non-leaking, matches
	// the ownership-scoped query pattern used elsewhere in this codebase.
	getOtherRequest := newAuthorNovelRequest(t, http.MethodGet, "/api/v1/author/novels/"+novel.ID, otherID, nil)
	getOtherRequest.SetPathValue("id", novel.ID)
	getOtherRecorder := httptest.NewRecorder()
	handler.Get(getOtherRecorder, getOtherRequest)
	if getOtherRecorder.Code != http.StatusNotFound {
		t.Errorf("other author Get status = %d, want 404, body = %s", getOtherRecorder.Code, getOtherRecorder.Body.String())
	}

	// A different author can't update it either.
	updateOtherRequest := newAuthorNovelRequest(t, http.MethodPut, "/api/v1/author/novels/"+novel.ID, otherID, novelWriteRequest{
		Title:      "Hijacked Title",
		AuthorName: "Hijacker",
		Status:     "ongoing",
	})
	updateOtherRequest.SetPathValue("id", novel.ID)
	updateOtherRecorder := httptest.NewRecorder()
	handler.Update(updateOtherRecorder, updateOtherRequest)
	if updateOtherRecorder.Code != http.StatusNotFound {
		t.Errorf("other author Update status = %d, want 404, body = %s", updateOtherRecorder.Code, updateOtherRecorder.Body.String())
	}

	// And the novel's list is scoped to its actual owner only.
	otherListRequest := newAuthorNovelRequest(t, http.MethodGet, "/api/v1/author/novels", otherID, nil)
	otherListRecorder := httptest.NewRecorder()
	handler.List(otherListRecorder, otherListRequest)
	var otherList struct {
		Items []novelResponse `json:"items"`
	}
	if err := json.Unmarshal(otherListRecorder.Body.Bytes(), &otherList); err != nil {
		t.Fatalf("unmarshal other author's list: %v", err)
	}
	for _, item := range otherList.Items {
		if item.ID == novel.ID {
			t.Error("other author's own-novels list leaked the first author's novel")
		}
	}
}

func ptrFloat64(value float64) *float64 { return &value }
