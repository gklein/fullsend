package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateIssueComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/repos/owner/repo/issues/42/comments", r.URL.Path)

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "Great work!", body["body"])

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{
			"id":         123,
			"body":       "Great work!",
			"user":       map[string]any{"login": "bot"},
			"created_at": "2026-01-01T00:00:00Z",
		})
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	comment, err := client.CreateIssueComment(context.Background(), "owner", "repo", 42, "Great work!")
	require.NoError(t, err)
	assert.Equal(t, 123, comment.ID)
	assert.Equal(t, "Great work!", comment.Body)
	assert.Equal(t, "bot", comment.Author)
}

func TestUpdateIssueComment(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "PATCH", r.Method)
		assert.Equal(t, "/repos/owner/repo/issues/comments/456", r.URL.Path)

		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		assert.Equal(t, "Updated body", body["body"])

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]any{
			"id":   456,
			"body": "Updated body",
		})
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	err := client.UpdateIssueComment(context.Background(), "owner", "repo", 456, "Updated body")
	require.NoError(t, err)
}

func TestListIssueComments_SinglePage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "GET", r.Method)
		assert.Equal(t, "/repos/owner/repo/issues/10/comments", r.URL.Path)
		assert.Equal(t, "100", r.URL.Query().Get("per_page"))
		assert.Equal(t, "1", r.URL.Query().Get("page"))

		json.NewEncoder(w).Encode([]map[string]any{
			{"id": 1, "body": "first", "user": map[string]any{"login": "alice"}, "created_at": "2026-01-01T00:00:00Z"},
			{"id": 2, "body": "second", "user": map[string]any{"login": "bob"}, "created_at": "2026-01-02T00:00:00Z"},
		})
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	comments, err := client.ListIssueComments(context.Background(), "owner", "repo", 10)
	require.NoError(t, err)
	require.Len(t, comments, 2)
	assert.Equal(t, 1, comments[0].ID)
	assert.Equal(t, "first", comments[0].Body)
	assert.Equal(t, "alice", comments[0].Author)
	assert.Equal(t, 2, comments[1].ID)
	assert.Equal(t, "second", comments[1].Body)
	assert.Equal(t, "bob", comments[1].Author)
}

func TestListIssueComments_Pagination(t *testing.T) {
	page := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page++
		assert.Equal(t, "GET", r.Method)

		switch page {
		case 1:
			assert.Equal(t, "1", r.URL.Query().Get("page"))
			// Return exactly 100 comments to trigger pagination.
			comments := make([]map[string]any, 100)
			for i := range comments {
				comments[i] = map[string]any{
					"id":         i + 1,
					"body":       "comment",
					"user":       map[string]any{"login": "bot"},
					"created_at": "2026-01-01T00:00:00Z",
				}
			}
			json.NewEncoder(w).Encode(comments)
		case 2:
			assert.Equal(t, "2", r.URL.Query().Get("page"))
			// Return fewer than 100 — pagination stops.
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": 101, "body": "last", "user": map[string]any{"login": "bot"}, "created_at": "2026-01-02T00:00:00Z"},
			})
		default:
			t.Fatalf("unexpected page %d requested", page)
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	comments, err := client.ListIssueComments(context.Background(), "owner", "repo", 5)
	require.NoError(t, err)
	assert.Len(t, comments, 101)
	assert.Equal(t, 2, page, "should have fetched exactly 2 pages")
	assert.Equal(t, 101, comments[100].ID)
	assert.Equal(t, "last", comments[100].Body)
}

func TestListIssueComments_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	comments, err := client.ListIssueComments(context.Background(), "owner", "repo", 999)
	assert.Error(t, err)
	assert.Nil(t, comments)
	assert.Contains(t, err.Error(), "list issue comments page 1")
}

func TestMinimizeComment(t *testing.T) {
	callNum := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callNum++
		switch callNum {
		case 1:
			// GET comment to retrieve node_id
			assert.Equal(t, "GET", r.Method)
			assert.Equal(t, "/repos/owner/repo/issues/comments/789", r.URL.Path)
			json.NewEncoder(w).Encode(map[string]any{
				"id":      789,
				"node_id": "IC_kwDOTest",
			})
		case 2:
			// POST GraphQL mutation
			assert.Equal(t, "POST", r.Method)
			assert.Equal(t, "/graphql", r.URL.Path)

			var body map[string]any
			json.NewDecoder(r.Body).Decode(&body)
			assert.Contains(t, body["query"], "minimizeComment")

			vars := body["variables"].(map[string]any)
			assert.Equal(t, "IC_kwDOTest", vars["id"])
			assert.Equal(t, "OUTDATED", vars["reason"])

			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{
					"minimizeComment": map[string]any{
						"minimizedComment": map[string]any{
							"isMinimized": true,
						},
					},
				},
			})
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	err := client.MinimizeComment(context.Background(), "owner", "repo", 789, "OUTDATED")
	require.NoError(t, err)
	assert.Equal(t, 2, callNum)
}

func TestMinimizeComment_GraphQLError(t *testing.T) {
	callNum := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callNum++
		switch callNum {
		case 1:
			// GET comment node_id — success.
			json.NewEncoder(w).Encode(map[string]any{
				"id":      789,
				"node_id": "IC_kwDOTest",
			})
		case 2:
			// POST GraphQL mutation — returns a GraphQL-level error.
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]any{
				"errors": []map[string]any{
					{"message": "Could not resolve to a node with the global id of 'IC_kwDOTest'"},
				},
			})
		}
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	err := client.MinimizeComment(context.Background(), "owner", "repo", 789, "OUTDATED")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "minimize comment 789")
	assert.Contains(t, err.Error(), "Could not resolve to a node")
}

func TestMinimizeComment_GetNodeIDError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]any{"message": "Not Found"})
	}))
	defer srv.Close()

	client := newTestClient(t, srv)
	err := client.MinimizeComment(context.Background(), "owner", "repo", 999, "OUTDATED")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get comment 999 for minimize")
}
