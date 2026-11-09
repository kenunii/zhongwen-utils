package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

var documentID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Status struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	Total    int    `json:"total"`
	Done     int    `json:"done"`
	Current  int    `json:"current"`
	Message  string `json:"message"`
	Hash     string `json:"hash"`
	Revision string `json:"revision"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}
type Processor interface {
	OCR(context.Context, []byte) (Source, error)
	Annotate(context.Context, Source) (Page, error)
}
type Manager struct {
	Documents, Cache string
	Cloud            Processor
	parallelPages    int
	mu               sync.Mutex
	active           string
	cancel           context.CancelFunc
	states           map[string]Status
}

func atomicJSON(path string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return atomicBytes(path, data)
}
func atomicBytes(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), path)
}
func newManager(documents, cache string, cloud Processor) (*Manager, error) {
	if err := os.MkdirAll(cache, 0700); err != nil {
		return nil, err
	}
	m := &Manager{Documents: documents, Cache: cache, Cloud: cloud, parallelPages: defaultParallelPages, states: map[string]Status{}}
	files, _ := filepath.Glob(filepath.Join(cache, "*", "status.json"))
	for _, path := range files {
		var s Status
		data, err := os.ReadFile(path)
		if err == nil && json.Unmarshal(data, &s) == nil && documentID.MatchString(s.ID) {
			if s.State == "running" {
				s.State = "error"
				s.Message = "Processing was interrupted. Tap to resume."
				_ = atomicJSON(path, s)
			}
			m.states[s.ID] = s
		}
	}
	return m, nil
}
func (m *Manager) document(id string) (string, error) {
	if !documentID.MatchString(id) {
		return "", fmt.Errorf("invalid document ID")
	}
	path := filepath.Join(m.Documents, id+".pdf")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("only downloaded PDF documents can be processed")
	}
	if info.Size() > 256<<20 {
		return "", fmt.Errorf("PDF exceeds the tablet processing limit (256 MiB)")
	}
	return path, nil
}
func (m *Manager) status(id string) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.states[id]
	if !ok {
		s = Status{ID: id, State: "idle"}
	}
	if s.Hash != "" {
		info, err := os.Stat(filepath.Join(m.Documents, id+".pdf"))
		if err != nil || info.Size() != s.Size || info.ModTime().UnixNano() != s.Modified {
			s.State = "stale"
			s.Message = "PDF changed. Process again to update lookups."
		}
	}
	return s
}
func (m *Manager) save(s *Status) error {
	s.Revision = strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := atomicJSON(filepath.Join(m.Cache, s.ID, "status.json"), s); err != nil {
		return err
	}
	m.mu.Lock()
	m.states[s.ID] = *s
	m.mu.Unlock()
	return nil
}
func (m *Manager) start(id string) (Status, error) {
	if _, err := m.document(id); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	if m.active != "" {
		s := m.states[m.active]
		same := m.active == id
		m.mu.Unlock()
		if same {
			return s, nil
		}
		return s, fmt.Errorf("another document is still processing")
	}
	s := m.states[id]
	s.ID = id
	s.State = "running"
	s.Message = "Preparing PDF"
	c, cancel := context.WithCancel(context.Background())
	m.active = id
	m.cancel = cancel
	m.states[id] = s
	m.mu.Unlock()
	if err := m.save(&s); err != nil {
		m.mu.Lock()
		m.active = ""
		m.cancel = nil
		m.mu.Unlock()
		cancel()
		return s, err
	}
	go func(s Status) {
		defer func() { m.mu.Lock(); m.active = ""; m.cancel = nil; m.mu.Unlock(); cancel() }()
		defer func() {
			if recover() != nil {
				s.State = "error"
				s.Message = "PDF processing failed. Original document is unchanged."
				_ = m.save(&s)
			}
		}()
		if err := m.process(c, &s); err != nil {
			if c.Err() != nil {
				s.State = "paused"
				s.Message = "Stopped. Tap to continue."
			} else {
				s.State = "error"
				s.Message = err.Error()
			}
			s.Current = 0
			_ = m.save(&s)
		}
	}(s)
	return s, nil
}

func (m *Manager) stop(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.active != id || m.cancel == nil {
		return Status{}, fmt.Errorf("document is not processing")
	}
	if m.states[id].State != "running" && m.states[id].State != "cancelling" {
		return Status{}, fmt.Errorf("document is no longer processing")
	}
	m.cancel()
	s := m.states[id]
	s.State = "cancelling"
	s.Message = "Stopping current requests"
	m.states[id] = s
	return s, nil
}
func (m *Manager) process(c context.Context, s *Status) error {
	if connection, ok := m.Cloud.(interface{ CloseSession() }); ok {
		defer connection.CloseSession()
	}
	path, err := m.document(s.ID)
	if err != nil {
		return err
	}
	input, err := os.Open(path)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, input); err != nil {
		return err
	}
	s.Hash = hex.EncodeToString(hash.Sum(nil))
	s.Size = info.Size()
	s.Modified = info.ModTime().UnixNano()
	dir := filepath.Join(m.Cache, s.ID, s.Hash)
	if err = os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if _, err = input.Seek(0, 0); err != nil {
		return err
	}
	// Parse once; split pages in memory and never modify the original PDF.
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed
	pdf, err := api.ReadValidateAndOptimize(c, input, conf, nil)
	if err != nil {
		return fmt.Errorf("cannot read PDF: %w", err)
	}
	s.Total = pdf.PageCount
	s.Done = 0
	for number := 1; number <= s.Total; number++ {
		if _, err = os.Stat(filepath.Join(dir, fmt.Sprintf("page-%03d.json", number))); err == nil {
			s.Done++
		}
	}
	if err = m.save(s); err != nil {
		return err
	}
	parallel := m.parallelPages
	if parallel < 1 {
		parallel = defaultParallelPages
	}
	var pending *pageTask
	for number := 1; number <= s.Total || pending != nil; {
		if err := c.Err(); err != nil {
			return err
		}
		batch := []pageTask{}
		preparedBytes := 0
		for (number <= s.Total || pending != nil) && len(batch) < parallel {
			if err := c.Err(); err != nil {
				return err
			}
			var task pageTask
			if pending != nil {
				task, pending = *pending, nil
			} else {
				pageNumber := number
				number++
				if _, err := os.Stat(pagePath(dir, pageNumber)); err == nil {
					continue
				}
				current, err := os.Stat(path)
				if err != nil || current.Size() != s.Size || current.ModTime().UnixNano() != s.Modified {
					return fmt.Errorf("PDF changed during processing; restart from the menu")
				}
				task = pageTask{number: pageNumber}
				if _, err := os.Stat(sourcePath(dir, pageNumber)); err != nil {
					// pdfcpu's shared document context is touched only by this goroutine.
					reader, err := api.ExtractPage(c, pdf, pageNumber)
					if err != nil {
						return fmt.Errorf("cannot prepare PDF page: %w", err)
					}
					task.pdf, err = io.ReadAll(io.LimitReader(reader, 7_000_001))
					if err != nil {
						return err
					}
				}
			}
			if len(batch) > 0 && preparedBytes+len(task.pdf) > maxPreparedPageBytes {
				pending = &task
				break
			}
			preparedBytes += len(task.pdf)
			batch = append(batch, task)
		}
		if len(batch) == 0 {
			continue
		}
		if err := m.processBatch(c, s, dir, batch); err != nil {
			return err
		}
	}
	if err := c.Err(); err != nil {
		return err
	}
	s.State = "complete"
	s.Current = 0
	s.Message = "Chinese lookup ready"
	return m.save(s)
}

type pageTask struct {
	number int
	pdf    []byte
}

type pageResult struct {
	number int
	err    error
}

func pagePath(dir string, number int) string {
	return filepath.Join(dir, fmt.Sprintf("page-%03d.json", number))
}
func sourcePath(dir string, number int) string {
	return filepath.Join(dir, fmt.Sprintf("source-%03d.json", number))
}

func (m *Manager) processPage(c context.Context, dir string, task pageTask) error {
	if err := c.Err(); err != nil {
		return err
	}
	var source Source
	file := sourcePath(dir, task.number)
	if data, err := os.ReadFile(file); err == nil {
		if err := json.Unmarshal(data, &source); err != nil {
			return err
		}
	} else {
		var err error
		source, err = m.Cloud.OCR(c, task.pdf)
		if err != nil {
			return fmt.Errorf("page %d OCR: %w", task.number, err)
		}
		if err := c.Err(); err != nil {
			return err
		}
		if err := atomicJSON(file, source); err != nil {
			return err
		}
	}
	if err := c.Err(); err != nil {
		return err
	}
	page, err := m.Cloud.Annotate(c, source)
	if err != nil {
		return fmt.Errorf("page %d: %w", task.number, err)
	}
	if err := c.Err(); err != nil {
		return err
	}
	return atomicJSON(pagePath(dir, task.number), page)
}

func (m *Manager) processBatch(c context.Context, s *Status, dir string, batch []pageTask) error {
	active := make(map[int]bool, len(batch))
	for _, task := range batch {
		active[task.number] = true
	}
	s.Current = batch[0].number
	s.Message = fmt.Sprintf("Processing %d pages in parallel", len(batch))
	if err := m.save(s); err != nil {
		return err
	}
	batchContext, cancel := context.WithCancel(c)
	defer cancel()
	results := make(chan pageResult, len(batch))
	for _, task := range batch {
		go func(task pageTask) {
			results <- pageResult{task.number, m.processPage(batchContext, dir, task)}
		}(task)
	}
	var firstError error
	for range batch {
		result := <-results
		delete(active, result.number)
		if result.err != nil && firstError == nil && c.Err() == nil {
			firstError = result.err
			cancel()
		}
		if result.err == nil {
			s.Done++
		}
		current := 0
		for number := range active {
			if current == 0 || number < current {
				current = number
			}
		}
		s.Current = current
		if c.Err() == nil && firstError == nil {
			s.Message = fmt.Sprintf("Processing %d pages in parallel", len(active))
			if err := m.save(s); err != nil {
				firstError = err
				cancel()
			}
		}
	}
	if err := c.Err(); err != nil {
		return err
	}
	return firstError
}
