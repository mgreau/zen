package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient("xoxp-test-token")
	c.baseURL = srv.URL + "/"
	return c
}

func requireBearer(t *testing.T, r *http.Request) {
	t.Helper()
	if got := r.Header.Get("Authorization"); got != "Bearer xoxp-test-token" {
		t.Errorf("Authorization header = %q, want %q", got, "Bearer xoxp-test-token")
	}
}

func TestListReactions_FiltersAndPaginates(t *testing.T) {
	page := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		if r.URL.Path != "/reactions.list" {
			t.Errorf("path = %q, want /reactions.list", r.URL.Path)
		}
		page++
		switch page {
		case 1:
			if r.URL.Query().Get("cursor") != "" {
				t.Errorf("first page should have no cursor, got %q", r.URL.Query().Get("cursor"))
			}
			w.Write([]byte(`{
				"ok": true,
				"items": [
					{"channel": "C1", "message": {"ts": "111.1", "reactions": [{"name": "claudecode", "count": 1}]}},
					{"channel": "C2", "message": {"ts": "222.2", "reactions": [{"name": "eyes", "count": 1}]}}
				],
				"response_metadata": {"next_cursor": "abc"}
			}`))
		case 2:
			if r.URL.Query().Get("cursor") != "abc" {
				t.Errorf("second page cursor = %q, want %q", r.URL.Query().Get("cursor"), "abc")
			}
			w.Write([]byte(`{
				"ok": true,
				"items": [
					{"channel": "C3", "message": {"ts": "333.3", "reactions": [{"name": "claudecode", "count": 1}]}}
				],
				"response_metadata": {"next_cursor": ""}
			}`))
		default:
			t.Fatalf("unexpected page %d", page)
		}
	})

	hits, err := client.ListReactions(context.Background(), "claudecode", 5)
	if err != nil {
		t.Fatalf("ListReactions() error: %v", err)
	}
	want := []ReactionHit{{ChannelID: "C1", MessageTS: "111.1"}, {ChannelID: "C3", MessageTS: "333.3"}}
	if len(hits) != len(want) {
		t.Fatalf("ListReactions() = %v, want %v", hits, want)
	}
	for i := range want {
		if hits[i] != want[i] {
			t.Errorf("hit %d = %v, want %v", i, hits[i], want[i])
		}
	}
	if page != 2 {
		t.Errorf("expected pagination to stop once next_cursor is empty, made %d requests", page)
	}
}

func TestListReactions_RespectsMaxPages(t *testing.T) {
	calls := 0
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Write([]byte(`{"ok": true, "items": [], "response_metadata": {"next_cursor": "keep-going"}}`))
	})

	if _, err := client.ListReactions(context.Background(), "claudecode", 3); err != nil {
		t.Fatalf("ListReactions() error: %v", err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (bounded by maxPages)", calls)
	}
}

func TestConversationsReplies(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		if r.URL.Path != "/conversations.replies" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("channel") != "C1" || r.URL.Query().Get("ts") != "111.1" {
			t.Errorf("unexpected query: %v", r.URL.Query())
		}
		w.Write([]byte(`{"ok": true, "messages": [
			{"user": "U1", "text": "parent", "ts": "111.1"},
			{"user": "U2", "text": "reply", "ts": "111.2"}
		]}`))
	})

	msgs, err := client.ConversationsReplies(context.Background(), "C1", "111.1")
	if err != nil {
		t.Fatalf("ConversationsReplies() error: %v", err)
	}
	if len(msgs) != 2 || msgs[0].Text != "parent" || msgs[1].Text != "reply" {
		t.Errorf("ConversationsReplies() = %+v", msgs)
	}
}

func TestAddReaction_AlreadyReactedIsNotAnError(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		if r.URL.Path != "/reactions.add" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["channel"] != "C1" || body["timestamp"] != "111.1" || body["name"] != "eyes" {
			t.Errorf("unexpected body: %v", body)
		}
		w.Write([]byte(`{"ok": false, "error": "already_reacted"}`))
	})

	if err := client.AddReaction(context.Background(), "C1", "111.1", "eyes"); err != nil {
		t.Errorf("AddReaction() with already_reacted should be nil, got %v", err)
	}
}

func TestAddReaction_OtherErrorPropagates(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"ok": false, "error": "channel_not_found"}`))
	})

	err := client.AddReaction(context.Background(), "C1", "111.1", "eyes")
	if err == nil {
		t.Fatal("expected error for channel_not_found")
	}
	apiErr, ok := err.(*APIError)
	if !ok || apiErr.Code != "channel_not_found" {
		t.Errorf("AddReaction() error = %v, want APIError{channel_not_found}", err)
	}
}

func TestPostMessage(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		if r.URL.Path != "/chat.postMessage" {
			t.Errorf("path = %q", r.URL.Path)
		}
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		if body["channel"] != "U08BR6P7000" || body["text"] != "hello" {
			t.Errorf("unexpected body: %v", body)
		}
		w.Write([]byte(`{"ok": true}`))
	})

	if err := client.PostMessage(context.Background(), "U08BR6P7000", "hello"); err != nil {
		t.Fatalf("PostMessage() error: %v", err)
	}
}

func TestAuthTest(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		w.Write([]byte(`{"ok": true, "user_id": "U08BR6P7000"}`))
	})

	userID, err := client.AuthTest(context.Background())
	if err != nil {
		t.Fatalf("AuthTest() error: %v", err)
	}
	if userID != "U08BR6P7000" {
		t.Errorf("AuthTest() = %q, want %q", userID, "U08BR6P7000")
	}
}

func TestHasReaction(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requireBearer(t, r)
		if r.URL.Path != "/reactions.get" {
			t.Errorf("path = %q", r.URL.Path)
		}
		if r.URL.Query().Get("channel") != "C1" || r.URL.Query().Get("timestamp") != "111.1" {
			t.Errorf("unexpected query: %v", r.URL.Query())
		}
		w.Write([]byte(`{"ok": true, "message": {"reactions": [{"name": "eyes", "count": 1}, {"name": "done_check", "count": 1}]}}`))
	})

	has, err := client.HasReaction(context.Background(), "C1", "111.1", "done_check")
	if err != nil {
		t.Fatalf("HasReaction() error: %v", err)
	}
	if !has {
		t.Error("HasReaction() = false, want true")
	}

	has, err = client.HasReaction(context.Background(), "C1", "111.1", "raised_hands")
	if err != nil {
		t.Fatalf("HasReaction() error: %v", err)
	}
	if has {
		t.Error("HasReaction() = true, want false for a reaction that isn't present")
	}
}

func TestPermalink(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat.getPermalink" {
			t.Errorf("path = %q", r.URL.Path)
		}
		w.Write([]byte(`{"ok": true, "permalink": "https://example.slack.com/archives/C1/p1111"}`))
	})

	link, err := client.Permalink(context.Background(), "C1", "111.1")
	if err != nil {
		t.Fatalf("Permalink() error: %v", err)
	}
	if link != "https://example.slack.com/archives/C1/p1111" {
		t.Errorf("Permalink() = %q", link)
	}
}
