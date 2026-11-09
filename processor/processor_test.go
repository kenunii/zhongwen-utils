package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func sampleSource() Source {
	return Source{Width: 2000, Height: 2800, Characters: []Character{{"学", Box{1500, 100, 50, 50}, 0}, {"习", Box{200, 200, 50, 50}, 0}}, Lines: []InputLine{{"L1", []string{"学习"}}}}
}

const answerJSON = `{"lines":[{"id":"L1","text":"学习","correction_reason":""}],"sentences":[{"translation":"Study.","words":[{"text":"学习","pinyin":"xué xí","meaning":"study"}]}]}`

func TestCompileWrappedWord(t *testing.T) {
	p, err := compile(sampleSource(), answerJSON)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Words) != 1 || len(p.Words[0].Boxes) != 2 {
		t.Fatalf("merged wrapped word: %+v", p)
	}
	for _, bad := range []string{strings.ReplaceAll(answerJSON, "学习", "学生"), strings.Replace(answerJSON, `"L1"`, `"L2"`, 1), answerJSON + `{}`, strings.Replace(answerJSON, `"pinyin":"xué xí"`, `"pinyin":""`, 1)} {
		if _, err = compile(sampleSource(), bad); err == nil {
			t.Fatal("accepted changed source or invalid output")
		}
	}
}
func TestExtraSentencePunctuation(t *testing.T) {
	extra := strings.Replace(answerJSON, `"meaning":"study"}]`, `"meaning":"study"},{"text":"。","pinyin":"","meaning":""}]`, 1)
	page, err := compile(sampleSource(), extra)
	if err != nil || len(page.Words) != 1 || page.Sentences[0].Text != "学习" {
		t.Fatalf("extra punctuation changed source alignment: %+v %v", page, err)
	}
	for _, bad := range []string{
		strings.Replace(extra, `"text":"。","pinyin":"","meaning":""`, `"text":"好","pinyin":"hǎo","meaning":"good"`, 1),
		strings.Replace(extra, `"text":"。","pinyin":"","meaning":""`, `"text":"。","pinyin":"x","meaning":""`, 1),
	} {
		if _, err := compile(sampleSource(), bad); err == nil {
			t.Fatal("accepted inserted or annotated token")
		}
	}
}
func TestSharedAnnotationCases(t *testing.T) {
	data, err := os.ReadFile("testdata/annotation_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string          `json:"name"`
		Text      string          `json:"text"`
		Sentences json.RawMessage `json:"sentences"`
		Valid     bool            `json:"valid"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			s := Source{Width: 1000, Height: 100, Lines: []InputLine{{"L1", []string{tc.Text}}}}
			for i, c := range []rune(tc.Text) {
				s.Characters = append(s.Characters, Character{string(c), Box{float64(i * 20), 0, 20, 20}, 0})
			}
			raw, err := json.Marshal(map[string]any{"lines": []map[string]string{{"id": "L1", "text": tc.Text, "correction_reason": ""}}, "sentences": tc.Sentences})
			if err != nil {
				t.Fatal(err)
			}
			page, err := compile(s, string(raw))
			if (err == nil) != tc.Valid {
				t.Fatalf("valid=%v, error=%v", tc.Valid, err)
			}
			if err == nil && (len(page.Sentences) != 1 || page.Sentences[0].Text != tc.Text) {
				t.Fatalf("source text changed: %+v", page.Sentences)
			}
		})
	}
}
func TestExerciseBlankNotMerged(t *testing.T) {
	s := sampleSource()
	s.Characters[0].Box = Box{10, 10, 20, 20}
	s.Characters[1].Box = Box{150, 10, 20, 20}
	if _, err := compile(s, answerJSON); err == nil {
		t.Fatal("word crossed blank")
	}
}
func TestNormalizedVisionGeometry(t *testing.T) {
	var p VisionPage
	if err := json.Unmarshal([]byte(`{"width":600,"height":800,"blocks":[{"paragraphs":[{"words":[{"symbols":[{"text":"学","boundingBox":{"normalizedVertices":[{"x":0.1,"y":0.1},{"x":0.2,"y":0.1},{"x":0.2,"y":0.2},{"x":0.1,"y":0.2}]}}]}]}]}]}`), &p); err != nil {
		t.Fatal(err)
	}
	s, err := visionSource(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Characters) != 1 || s.Characters[0].Box[0] != 200 || s.Lines[0].Parts[0] != "学" {
		t.Fatalf("wrong mapping: %+v", s)
	}
}

// Synthetic pages only: no private document is used by the tests.
func testPDF() []byte { return testPDFPages(2) }
func testPDFPages(count int) []byte {
	kids := make([]string, count)
	for i := range kids {
		kids[i] = fmt.Sprintf("%d 0 R", i+3)
	}
	content := count + 3
	objects := []string{"<< /Type /Catalog /Pages 2 0 R >>", fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), count)}
	for range count {
		objects = append(objects, fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 600 800] /Resources << >> /Contents %d 0 R >>", content))
	}
	objects = append(objects, "<< /Length 0 >>\nstream\n\nendstream")
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offsets := []int{0}
	for i, s := range objects {
		offsets = append(offsets, b.Len())
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, s)
	}
	xref := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(offsets))
	for _, off := range offsets[1:] {
		fmt.Fprintf(&b, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&b, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xref)
	return b.Bytes()
}

type fakeCloud struct {
	mu               sync.Mutex
	ocr, annotations int
	fail             bool
}

func (f *fakeCloud) OCR(_ context.Context, _ []byte) (Source, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ocr++
	return sampleSource(), nil
}
func (f *fakeCloud) Annotate(_ context.Context, s Source) (Page, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.annotations++
	if f.fail && f.annotations == 2 {
		return Page{}, fmt.Errorf("simulated failure")
	}
	return compile(s, answerJSON)
}

const testID = "00000000-0000-4000-8000-000000000001"

func waitJob(t *testing.T, m *Manager) Status {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for time.Now().Before(end) {
		m.mu.Lock()
		active := m.active
		m.mu.Unlock()
		if active == "" {
			return m.status(testID)
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("job did not finish")
	return Status{}
}
func TestResumeKeepsFinishedPagesAndOriginal(t *testing.T) {
	docs := t.TempDir()
	original := testPDF()
	path := filepath.Join(docs, testID+".pdf")
	os.WriteFile(path, original, 0600)
	cloud := &fakeCloud{fail: true}
	m, err := newManager(docs, t.TempDir(), cloud)
	if err != nil {
		t.Fatal(err)
	}
	m.parallelPages = 1 // The legacy retry fixture exercises one failed page at a time.
	if _, err = m.start(testID); err != nil {
		t.Fatal(err)
	}
	s := waitJob(t, m)
	if s.State != "error" || s.Done != 1 {
		t.Fatalf("unexpected failure state: %+v", s)
	}
	if _, err = m.start(testID); err != nil {
		t.Fatal(err)
	}
	s = waitJob(t, m)
	if s.State != "complete" || s.Done != 2 {
		t.Fatalf("resume failed: %+v", s)
	}
	cloud.mu.Lock()
	ocr, annotations := cloud.ocr, cloud.annotations
	cloud.mu.Unlock()
	if ocr != 2 || annotations != 3 {
		t.Fatalf("repeated completed calls: OCR %d, annotations %d", ocr, annotations)
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(original, after) {
		t.Fatal("original PDF changed")
	}
	if _, err = m.start(testID); err != nil {
		t.Fatal(err)
	}
	waitJob(t, m)
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if cloud.ocr != 2 || cloud.annotations != 3 {
		t.Fatal("completed document was charged again")
	}
}
func TestLocalHTTPBoundaries(t *testing.T) {
	docs := t.TempDir()
	os.WriteFile(filepath.Join(docs, testID+".pdf"), testPDF(), 0600)
	m, _ := newManager(docs, t.TempDir(), &fakeCloud{})
	handler := m.handler()
	for _, c := range []struct {
		method, path, origin string
		code                 int
	}{{"POST", "/v1/documents/" + testID + "/process", "", 403}, {"GET", "/v1/documents/" + testID, "https://example.com", 403}, {"GET", "/v1/documents/not-a-uuid", "", 404}, {"GET", "/v1/documents/" + testID, "", 200}} {
		req := httptest.NewRequest(c.method, c.path, nil)
		req.Header.Set("Origin", c.origin)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != c.code {
			t.Fatalf("%s: %d", c.path, w.Code)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestCloudErrorDoesNotLeakResponseOrRetry(t *testing.T) {
	c := newCloud(Credentials{Vision: "private-key", DeepSeek: "private-key"})
	calls := 0
	c.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: 403, Body: io.NopCloser(strings.NewReader("sensitive provider response"))}, nil
	})
	_, err := c.OCR(context.Background(), testPDF())
	if err == nil || strings.Contains(err.Error(), "sensitive") || calls != 1 {
		t.Fatal("unsafe error or retry")
	}
}

func TestPDFWordGeometryWithoutSymbolBoxes(t *testing.T) {
	w := VisionWord{Symbols: []Symbol{{Text: "学"}, {Text: "习"}}, Bounding: Polygon{Normalized: []Vertex{{.1, .1}, {.3, .1}, {.3, .2}, {.1, .2}}}}
	symbols := locatedSymbols(w)
	a, err := symbolBox(symbols[0], 600, 800)
	if err != nil {
		t.Fatal(err)
	}
	b, err := symbolBox(symbols[1], 600, 800)
	if err != nil {
		t.Fatal(err)
	}
	if a[0] != 200 || a[2] > 201 || b[0] < 399 || b[2] > 201 {
		t.Fatalf("incorrect word subdivision: %v %v", a, b)
	}
}

func TestBlankCloudPage(t *testing.T) {
	c := newCloud(Credentials{})
	c.Client.Transport = roundTrip(func(_ *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{"responses":[{"error":null,"responses":[{"error":null}]}]}`))}, nil
	})
	source, err := c.OCR(context.Background(), testPDF())
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.Annotate(context.Background(), source)
	if err != nil || len(page.Words) != 0 || page.Width <= 0 {
		t.Fatalf("blank page failed: %v", err)
	}
}

type waitingCloud struct {
	mu                  sync.Mutex
	calls, active, peak int
	started             chan struct{}
	release             chan struct{}
}

func (f *waitingCloud) OCR(c context.Context, _ []byte) (Source, error) {
	f.mu.Lock()
	f.calls++
	f.active++
	if f.active > f.peak {
		f.peak = f.active
	}
	f.mu.Unlock()
	defer func() { f.mu.Lock(); f.active--; f.mu.Unlock() }()
	select {
	case f.started <- struct{}{}:
	default:
	}
	select {
	case <-f.release:
		return sampleSource(), nil
	case <-c.Done():
		return Source{}, c.Err()
	}
}
func (f *waitingCloud) Annotate(_ context.Context, s Source) (Page, error) {
	return compile(s, answerJSON)
}

func TestTenConcurrentPagesCanBeStoppedAndResumed(t *testing.T) {
	docs := t.TempDir()
	original := testPDFPages(12)
	path := filepath.Join(docs, testID+".pdf")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	cloud := &waitingCloud{started: make(chan struct{}, 12), release: make(chan struct{})}
	m, err := newManager(docs, t.TempDir(), cloud)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.start(testID); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		select {
		case <-cloud.started:
		case <-time.After(5 * time.Second):
			t.Fatal("ten pages did not start")
		}
	}
	cloud.mu.Lock()
	peak := cloud.peak
	cloud.mu.Unlock()
	if peak != 10 {
		t.Fatalf("parallel limit was %d, want 10", peak)
	}
	req := httptest.NewRequest("POST", "/v1/documents/"+testID+"/cancel", nil)
	req.Header.Set("X-DuChinese-Local", "1")
	w := httptest.NewRecorder()
	m.handler().ServeHTTP(w, req)
	if w.Code != 202 {
		t.Fatalf("cancel returned %d", w.Code)
	}
	var stopping Status
	if err := json.Unmarshal(w.Body.Bytes(), &stopping); err != nil || stopping.State != "cancelling" {
		t.Fatalf("wrong stop result: %+v %v", stopping, err)
	}
	s := waitJob(t, m)
	if s.State != "paused" || s.Done != 0 {
		t.Fatalf("cancel did not pause: %+v", s)
	}
	cloud.mu.Lock()
	firstCalls := cloud.calls
	cloud.mu.Unlock()
	if firstCalls != 10 {
		t.Fatalf("queued more than ten pages: %d", firstCalls)
	}
	close(cloud.release)
	if _, err = m.start(testID); err != nil {
		t.Fatal(err)
	}
	s = waitJob(t, m)
	if s.State != "complete" || s.Done != 12 {
		t.Fatalf("resume failed: %+v", s)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("original PDF changed")
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if cloud.peak > 10 {
		t.Fatalf("concurrent requests exceeded limit: %d", cloud.peak)
	}
}

type firstPageCloud struct {
	mu               sync.Mutex
	ocr, annotations int
	blocked          chan struct{}
	entered          chan struct{}
}

func (f *firstPageCloud) OCR(_ context.Context, _ []byte) (Source, error) {
	f.mu.Lock()
	f.ocr++
	f.mu.Unlock()
	return sampleSource(), nil
}
func (f *firstPageCloud) Annotate(c context.Context, s Source) (Page, error) {
	f.mu.Lock()
	f.annotations++
	n := f.annotations
	f.mu.Unlock()
	select {
	case f.entered <- struct{}{}:
	default:
	}
	if n > 1 {
		select {
		case <-f.blocked:
		case <-c.Done():
			return Page{}, c.Err()
		}
	}
	return compile(s, answerJSON)
}
func TestCancelKeepsCompletedPagesAndOCRCache(t *testing.T) {
	docs := t.TempDir()
	os.WriteFile(filepath.Join(docs, testID+".pdf"), testPDFPages(10), 0600)
	cloud := &firstPageCloud{blocked: make(chan struct{}), entered: make(chan struct{}, 10)}
	m, err := newManager(docs, t.TempDir(), cloud)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.start(testID); err != nil {
		t.Fatal(err)
	}
	for range 10 {
		select {
		case <-cloud.entered:
		case <-time.After(5 * time.Second):
			t.Fatal("annotation did not start")
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for m.status(testID).Done < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if m.status(testID).Done != 1 {
		t.Fatal("first page was not saved")
	}
	if _, err := m.stop(testID); err != nil {
		t.Fatal(err)
	}
	s := waitJob(t, m)
	if s.State != "paused" || s.Done != 1 {
		t.Fatalf("stopped pages not preserved: %+v", s)
	}
	close(cloud.blocked)
	if _, err := m.start(testID); err != nil {
		t.Fatal(err)
	}
	s = waitJob(t, m)
	if s.State != "complete" || s.Done != 10 {
		t.Fatalf("resume failed: %+v", s)
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if cloud.ocr != 10 {
		t.Fatalf("repeat OCR on cached page: %d calls", cloud.ocr)
	}
}
