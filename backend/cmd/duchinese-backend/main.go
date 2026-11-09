package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kenunii/zhongwen-utils/backend/internal/appload"
	"github.com/kenunii/zhongwen-utils/backend/internal/duchinese"
	"github.com/kenunii/zhongwen-utils/backend/internal/progress"
)

const (
	requestBootstrap     = uint32(1)
	requestTop           = uint32(2)
	requestLatest        = uint32(3)
	requestSearch        = uint32(4)
	requestLesson        = uint32(5)
	requestCourse        = uint32(6)
	requestProgress      = uint32(7)
	requestStudied       = uint32(8)
	requestMarkRead      = uint32(9)
	requestSettings      = uint32(10)
	requestFinishStats   = uint32(11)
	requestSaved         = uint32(12)
	requestDownload      = uint32(13)
	requestDownloaded    = uint32(14)
	requestContinue      = uint32(15)
	requestCourseContext = uint32(16)

	responseState    = uint32(101)
	responseData     = uint32(102)
	responseProgress = uint32(103)
	responseControl  = uint32(104)
	responseError    = uint32(199)
)

type request struct {
	Query        string `json:"query"`
	Page         int    `json:"page"`
	Position     int    `json:"position"`
	Path         string `json:"path"`
	Title        string `json:"title"`
	Level        string `json:"level"`
	Completed    bool   `json:"completed"`
	ID           string `json:"id"`
	CoursePath   string `json:"course_path"`
	CourseTitle  string `json:"course_title"`
	ChapterLabel string `json:"chapter_label"`
}

func main() {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	configDir := filepath.Join(home, ".config", "duchinese-remarkable")
	controlPath := filepath.Join(configDir, "deployment-control.sock")
	if len(os.Args) == 3 && os.Args[1] == "--request-exit" {
		pid, err := strconv.Atoi(os.Args[2])
		if err != nil || pid <= 0 {
			log.Fatal("invalid backend PID")
		}
		if err := requestControlledExit(controlPath, pid); err != nil {
			log.Fatal(err)
		}
		fmt.Println("DuChinese was asked to exit through AppLoad.")
		return
	}
	if len(os.Args) != 2 {
		log.Fatal("usage: duchinese-backend <AppLoad socket>")
	}
	client, err := duchinese.New(
		filepath.Join(configDir, "session.json"),
		filepath.Join(configDir, "mobile-session.json"),
	)
	if err != nil {
		log.Fatal(err)
	}
	progressStore, err := progress.Open(filepath.Join(configDir, "progress.json"))
	if err != nil {
		log.Fatal(err)
	}
	conn, err := appload.Dial(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	control, controlErr := serveExitControl(controlPath, os.Getpid(), func() error {
		return sendJSON(conn, responseControl, map[string]string{"action": "exit"})
	})
	if controlErr != nil {
		log.Printf("deployment exit control unavailable: %v", controlErr)
	} else {
		defer control.Close()
	}

	for {
		message, err := conn.Receive()
		if err != nil {
			return
		}
		if message.Type == appload.Terminate {
			return
		}
		if message.Type == appload.NewCoordinator || message.Type == appload.LostCoordinator {
			continue
		}
		if err := handle(conn, client, progressStore, message); err != nil {
			sendError(conn, err)
		}
	}
}

func handle(conn *appload.Connection, client *duchinese.Client, progressStore *progress.Store, message appload.Message) error {
	var req request
	if len(message.Contents) > 0 {
		if err := json.Unmarshal(message.Contents, &req); err != nil {
			return fmt.Errorf("invalid request: %w", err)
		}
	}
	switch message.Type {
	case requestBootstrap:
		return sendJSON(conn, responseState, map[string]any{
			"authenticated": client.Ready(), "mobile_authenticated": client.MobileReady(),
			"progress": progressStore.State(), "downloaded_count": client.DownloadedCount(),
		})
	case requestTop:
		payload, err := client.Top()
		return sendRaw(conn, "top", payload, err)
	case requestLatest:
		payload, err := client.Latest(max(1, req.Page))
		return sendRaw(conn, "latest", payload, err)
	case requestSaved:
		payload, err := client.Saved(max(1, req.Page))
		return sendRaw(conn, "saved", payload, err)
	case requestDownloaded:
		payload, err := client.Downloaded()
		return sendRaw(conn, "downloaded", payload, err)
	case requestContinue:
		// The sync response can be relatively large. Keep the local bootstrap and
		// library usable while the official cross-device state is fetched.
		go func() {
			remote, err := client.RemoteContinueReading()
			if err != nil {
				log.Printf("continue reading sync: %v", err)
				_ = sendJSON(conn, responseData, map[string]any{"kind": "continue", "payload": nil})
				return
			}
			_ = sendJSON(conn, responseData, map[string]any{"kind": "continue", "payload": remote})
		}()
		return nil
	case requestDownload:
		if req.Path == "" {
			return errors.New("course download requires a path")
		}
		count, err := client.DownloadCourse(req.Path)
		if err != nil {
			return err
		}
		downloaded, cachedPaths := client.CourseCacheStatus(req.Path)
		if err := sendJSON(conn, responseData, map[string]any{
			"kind": "download_course", "payload": map[string]any{
				"count": count, "downloaded": downloaded, "cached_paths": cachedPaths,
			},
		}); err != nil {
			return err
		}
		go refreshStudiedInBackground(conn, client, progressStore)
		return nil
	case requestSearch:
		payload, err := client.Search(req.Query, max(1, req.Page))
		return sendRaw(conn, "search", payload, err)
	case requestLesson:
		started := time.Now()
		defer func() { log.Printf("[DuChinese] lesson backend response: %s", time.Since(started)) }()
		var persistedReaderURL string
		progressEntry, hasProgress := progressStore.Entry(req.Path)
		if hasProgress {
			persistedReaderURL = progressEntry.ReaderURL
		}
		payload, err := client.OpenLesson(req.Path, persistedReaderURL)
		if errors.Is(err, duchinese.ErrNoReaderData) && hasProgress && progressEntry.Title != "" {
			if _, searchErr := client.Search(progressEntry.Title, 1); searchErr == nil {
				payload, err = client.OpenLesson(req.Path)
			}
		}
		if err == nil {
			var opened struct {
				ReaderURL string `json:"reader_url"`
			}
			if json.Unmarshal(payload, &opened) == nil && opened.ReaderURL != "" &&
				(!hasProgress || progressEntry.ReaderURL != opened.ReaderURL) {
				go func(path, readerURL string) {
					if err := progressStore.RememberReader(path, readerURL); err != nil {
						log.Printf("remember reader URL for %s: %v", path, err)
					}
				}(req.Path, opened.ReaderURL)
			}
		}
		return sendRaw(conn, "lesson", payload, err)
	case requestCourse:
		payload, err := client.Course(req.Path)
		if err != nil {
			return err
		}
		downloaded, cachedPaths := client.CourseCacheStatus(req.Path)
		return sendJSON(conn, responseData, map[string]any{
			"kind": "course", "payload": payload,
			"downloaded": downloaded, "cached_paths": cachedPaths,
		})
	case requestCourseContext:
		if req.Path == "" {
			return errors.New("course context requires a path")
		}
		go func(path string) {
			payload, err := client.Course(path)
			if err != nil {
				log.Printf("load course context %s: %v", path, err)
				return
			}
			downloaded, cachedPaths := client.CourseCacheStatus(path)
			if err := sendJSON(conn, responseData, map[string]any{
				"kind": "course_context", "path": path, "payload": payload,
				"downloaded": downloaded, "cached_paths": cachedPaths,
			}); err != nil {
				log.Printf("send course context %s: %v", path, err)
			}
		}(req.Path)
		return nil
	case requestProgress:
		if req.Path == "" || req.Title == "" {
			return errors.New("progress requires a lesson path and title")
		}
		state, err := progressStore.Update(progress.Entry{
			Path: req.Path, ID: req.ID, Title: req.Title, Level: req.Level,
			CoursePath: req.CoursePath, CourseTitle: req.CourseTitle, ChapterLabel: req.ChapterLabel,
			Page: req.Page, Position: req.Position, Completed: req.Completed,
		})
		if err != nil {
			return err
		}
		return sendJSON(conn, responseProgress, state)
	case requestStudied:
		go refreshStudiedInBackground(conn, client, progressStore)
		return nil
	case requestMarkRead:
		if err := client.MarkStudied(req.ID); err != nil {
			if queueErr := progressStore.QueueStudied(req.ID); queueErr != nil {
				return queueErr
			}
		} else if err := progressStore.ResolveStudied(req.ID); err != nil {
			return err
		}
		if err := progressStore.RememberStudied(req.ID); err != nil {
			return err
		}
		return sendJSON(conn, responseProgress, map[string]any{"studied_id": req.ID, "progress": progressStore.State(), "pending_sync": contains(progressStore.PendingStudiedIDs(), req.ID)})
	case requestSettings:
		if !validLevelFilter(req.Level) {
			return errors.New("invalid level filter")
		}
		state, err := progressStore.SetLevelFilter(req.Level)
		if err != nil {
			return err
		}
		return sendJSON(conn, responseProgress, state)
	case requestFinishStats:
		if req.ID == "" {
			return errors.New("reading statistics require a lesson ID")
		}
		needsSync := !req.Completed || contains(progressStore.PendingStudiedIDs(), req.ID)
		if needsSync {
			if err := progressStore.QueueStudied(req.ID); err != nil {
				return err
			}
		}
		if err := progressStore.RememberStudied(req.ID); err != nil {
			return err
		}
		if err := sendJSON(conn, responseProgress, map[string]any{
			"studied_id": req.ID, "progress": progressStore.State(), "pending_sync": needsSync,
		}); err != nil {
			return err
		}
		go finishLessonInBackground(conn, client, progressStore, req.ID, needsSync)
		return nil
	default:
		return errors.New("unknown request")
	}
}

type finishClient interface {
	MarkStudied(string) error
	MobileReady() bool
	FinishedReadingStats([]string) (json.RawMessage, error)
}

func refreshStudiedInBackground(conn *appload.Connection, client *duchinese.Client, store *progress.Store) {
	payload, err := client.StudiedLessonIDs()
	if err != nil {
		log.Printf("refresh studied lessons: %v", err)
		return
	}
	pending := store.PendingStudiedIDs()
	for _, id := range pending {
		if client.MarkStudied(id) == nil {
			if err := store.ResolveStudied(id); err != nil {
				log.Printf("resolve studied lesson %s: %v", id, err)
			}
		}
	}
	ids, err := store.MergeStudiedIDs(studiedStrings(payload))
	if err != nil {
		log.Printf("save studied lessons: %v", err)
		return
	}
	payload = mergeStudied(payload, ids)
	if err := sendRaw(conn, "studied", payload, nil); err != nil {
		log.Printf("send studied lessons: %v", err)
	}
}

func finishLessonInBackground(conn *appload.Connection, client finishClient, store *progress.Store, id string, needsSync bool) {
	payload := finishLesson(client, store, id, needsSync)
	if err := sendJSON(conn, responseData, map[string]any{
		"kind": "finish_stats", "lesson_id": id, "payload": payload,
	}); err != nil {
		log.Printf("send reading statistics for lesson %s: %v", id, err)
	}
}

func finishLesson(client finishClient, store *progress.Store, id string, needsSync bool) any {
	if needsSync {
		if err := client.MarkStudied(id); err != nil {
			log.Printf("mark lesson %s as studied: %v", id, err)
		} else if err := store.ResolveStudied(id); err != nil {
			log.Printf("resolve studied lesson %s: %v", id, err)
		}
	}
	pending := contains(store.PendingStudiedIDs(), id)
	result := map[string]any{
		"chart_data": []any{}, "new_words": []any{}, "learned_words": []any{},
		"new_count": -1, "learned_count": -1,
	}
	if pending {
		result["pending_sync"] = true
	} else if client.MobileReady() {
		ids := store.RecentCompletedIDs(3)
		if len(ids) == 0 {
			ids = []string{id}
		}
		payload, err := client.FinishedReadingStats(ids)
		if err != nil {
			log.Printf("reading statistics for lesson %s: %v", id, err)
			result["stats_unavailable"] = true
		} else {
			return payload
		}
	}
	return result
}

func mergeStudied(payload json.RawMessage, pending []string) json.RawMessage {
	var ids []any
	if json.Unmarshal(payload, &ids) != nil {
		return payload
	}
	seen := make(map[string]bool, len(ids)+len(pending))
	for _, id := range ids {
		seen[fmt.Sprint(id)] = true
	}
	for _, id := range pending {
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	merged, err := json.Marshal(ids)
	if err != nil {
		return payload
	}
	return merged
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func studiedStrings(payload json.RawMessage) []string {
	var values []any
	if json.Unmarshal(payload, &values) != nil {
		return nil
	}
	ids := make([]string, 0, len(values))
	for _, value := range values {
		id := fmt.Sprint(value)
		if id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

func validLevelFilter(level string) bool {
	switch level {
	case "", "newbie", "elementary", "intermediate", "upper intermediate", "advanced", "master":
		return true
	default:
		return false
	}
}

func sendRaw(conn *appload.Connection, kind string, payload json.RawMessage, err error) error {
	if err != nil {
		return err
	}
	message := append([]byte(`{"kind":`+fmt.Sprintf("%q", kind)+`,"payload":`), payload...)
	message = append(message, '}')
	return conn.Send(responseData, message)
}

func sendJSON(conn *appload.Connection, typ uint32, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return conn.Send(typ, data)
}

func sendError(conn *appload.Connection, err error) {
	code := "request_failed"
	if errors.Is(err, duchinese.ErrNotAuthenticated) {
		code = "not_authenticated"
	}
	_ = sendJSON(conn, responseError, map[string]string{"code": code, "message": err.Error()})
}
