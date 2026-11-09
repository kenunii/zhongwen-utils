package main

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/kenunii/zhongwen-utils/backend/internal/progress"
)

type fakeFinishClient struct {
	mark  func(string) error
	stats func([]string) (json.RawMessage, error)
}

func (c fakeFinishClient) MarkStudied(id string) error { return c.mark(id) }
func (c fakeFinishClient) MobileReady() bool           { return c.stats != nil }
func (c fakeFinishClient) FinishedReadingStats(ids []string) (json.RawMessage, error) {
	return c.stats(ids)
}

func TestFinishLessonKeepsLocalProgressAvailableDuringSlowSync(t *testing.T) {
	store, err := progress.Open(filepath.Join(t.TempDir(), "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(progress.Entry{Path: "/first", ID: "42", Title: "First", Completed: true}); err != nil {
		t.Fatal(err)
	}
	if err := store.QueueStudied("42"); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan any, 1)
	client := fakeFinishClient{
		mark: func(id string) error {
			if id != "42" {
				t.Errorf("marked %q, want 42", id)
			}
			close(started)
			<-release
			return nil
		},
		stats: func(ids []string) (json.RawMessage, error) {
			if len(ids) != 1 || ids[0] != "42" {
				t.Errorf("stats IDs = %v, want [42]", ids)
			}
			return json.RawMessage(`{"new_count":2}`), nil
		},
	}
	go func() { done <- finishLesson(client, store, "42", true) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background sync did not start")
	}
	updated := make(chan error, 1)
	go func() {
		_, err := store.Update(progress.Entry{Path: "/next", ID: "43", Title: "Next"})
		updated <- err
	}()
	select {
	case err := <-updated:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow sync blocked progress for the next chapter")
	}
	close(release)
	select {
	case result := <-done:
		if string(result.(json.RawMessage)) != `{"new_count":2}` {
			t.Fatalf("stats = %s", result)
		}
	case <-time.After(time.Second):
		t.Fatal("background sync did not complete")
	}
	if pending := store.PendingStudiedIDs(); len(pending) != 0 {
		t.Fatalf("pending sync after success = %v", pending)
	}
}

func TestFinishLessonKeepsOfflineReadQueued(t *testing.T) {
	store, err := progress.Open(filepath.Join(t.TempDir(), "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.QueueStudied("42"); err != nil {
		t.Fatal(err)
	}
	client := fakeFinishClient{mark: func(string) error { return errors.New("offline") }}
	result := finishLesson(client, store, "42", true).(map[string]any)
	if result["pending_sync"] != true {
		t.Fatalf("offline result = %v", result)
	}
	if pending := store.PendingStudiedIDs(); len(pending) != 1 || pending[0] != "42" {
		t.Fatalf("pending sync = %v, want [42]", pending)
	}
}
