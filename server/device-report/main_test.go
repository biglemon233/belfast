// 钉住签名、防重放、去敏、持久去重和 GitHub 不确定响应的行为。
//
// Verifies signatures, replay rejection, redaction, persistent deduplication and reconciliation.
package main

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func validReport() Report {
	return Report{Backend: "SHIZUKU", RunMode: "BACKGROUND",
		SchemaVersion: 2, Manufacturer: "Example", Brand: "Example",
		MarketingName: "Example Phone", RomName: "Android", RomVersion: "16",
		BuildDisplay: "TEST.1", BuildIncremental: "16.1",
		Model: "Test Model", Device: "test", Product: "test", Hardware: "test",
		AndroidRelease: "16", SdkInt: 36, SecurityPatch: "2026-09-01",
		SupportedAbis: []string{"arm64-v8a"}, ScreenWidth: 1080, ScreenHeight: 2400,
		DensityDpi: 420, RefreshRate: 120, MemoryGiB: 8, AppVersion: "1.2.58",
		AppVersionCode: 100, Compatibility: "working"}
}

func fixture(t *testing.T, githubHandler http.HandlerFunc) (*service, *rsa.PrivateKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	state, err := openStore(filepath.Join(t.TempDir(), "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(githubHandler)
	t.Cleanup(upstream.Close)
	return &service{
		cert: &x509.Certificate{PublicKey: &key.PublicKey, NotBefore: now.Add(-time.Hour),
			NotAfter: now.Add(time.Hour)},
		github: githubClient{client: upstream.Client(), baseURL: upstream.URL, repository: "owner/repo", token: "test"},
		state:  state, gate: make(chan struct{}, 1), nonces: map[string]time.Time{}, now: func() time.Time { return now },
	}, key
}

func signedRequest(t *testing.T, s *service, key *rsa.PrivateKey, body []byte) *http.Request {
	t.Helper()
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	ts := strconv.FormatInt(s.now().Unix(), 10)
	nonceText := hex.EncodeToString(nonce)
	message := append([]byte("POST\n/v1/device-reports\n"+ts+"\n"+nonceText+"\n"), body...)
	digest := sha256.Sum256(message)
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/device-reports", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Report-Timestamp", ts)
	r.Header.Set("X-Report-Nonce", nonceText)
	r.Header.Set("X-Report-Signature", base64.StdEncoding.EncodeToString(signature))
	return r
}

func TestAuthenticationAndValidation(t *testing.T) {
	s, key := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid report reached github")
		w.WriteHeader(500)
	})
	body, _ := json.Marshal(validReport())
	tests := []struct {
		name   string
		mutate func(*http.Request)
		body   []byte
		status int
	}{
		{"unsigned", func(r *http.Request) { r.Header.Del("X-Report-Signature") }, body, 401},
		{"tampered", func(r *http.Request) { r.Body = http.NoBody }, body, 401},
		{"stale", func(r *http.Request) { r.Header.Set("X-Report-Timestamp", "1") }, body, 401},
		{"unique identifier", func(*http.Request) {}, []byte(`{"imei":"123456789012345"}`), 400},
		{"trailing JSON", func(*http.Request) {}, append(append([]byte{}, body...), []byte("{}")...), 400},
		{"oversized", func(*http.Request) {}, bytes.Repeat([]byte("a"), 8193), 413},
		{"method", func(r *http.Request) { r.Method = http.MethodGet }, body, 405},
		{"content type", func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, body, 415},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := signedRequest(t, s, key, tt.body)
			tt.mutate(r)
			w := httptest.NewRecorder()
			s.ServeHTTP(w, r)
			if w.Code != tt.status {
				t.Fatalf("got %d, want %d: %s", w.Code, tt.status, w.Body)
			}
		})
	}
	r := signedRequest(t, s, key, []byte("{}"))
	if !s.authenticate(r, []byte("{}")) || s.authenticate(r, []byte("{}")) {
		t.Fatal("nonce replay was not rejected")
	}
	s.cert.NotAfter = s.now().Add(-time.Second)
	if s.authenticate(signedRequest(t, s, key, body), body) {
		t.Fatal("expired certificate accepted")
	}
}

func TestPersistentDeduplicationAndRedaction(t *testing.T) {
	posts := 0
	s, key := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		posts++
		if r.Method != http.MethodPost || r.URL.Path != "/repos/owner/repo/issues/1/comments" {
			t.Fatal("report was not appended to the support issue")
		}
		var issue map[string]string
		if err := json.NewDecoder(r.Body).Decode(&issue); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(issue["body"], "123456789012345") {
			t.Fatal("sensitive value was published")
		}
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Fatal("missing github auth")
		}
		fmt.Fprint(w, `{"html_url":"https://github.com/owner/repo/issues/1#issuecomment-4000000007","id":4000000007}`)
	})
	report := validReport()
	report.Hardware = "imei=123456789012345"
	body, _ := json.Marshal(report)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, signedRequest(t, s, key, body))
	if w.Code != 201 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	reopened, err := openStore(s.state.path)
	if err != nil {
		t.Fatal(err)
	}
	s.state = reopened
	report.AppVersion = "1.3.0"
	report.AppVersionCode = 101
	body, _ = json.Marshal(report)
	w = httptest.NewRecorder()
	s.ServeHTTP(w, signedRequest(t, s, key, body))
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duplicate":true`) || posts != 1 {
		t.Fatalf("dedup: %d %s; posts=%d", w.Code, w.Body, posts)
	}
	for _, value := range []string{"serial=ABCD", "AA:BB:CC:DD:EE:FF", "user@example.org",
		"abc 123456789012345", "550e8400-e29b-41d4-a716-446655440000"} {
		if cleanText(value) != "[redacted]" {
			t.Fatalf("not redacted: %s", value)
		}
	}
}

func TestPendingCommentReconciliation(t *testing.T) {
	report := validReport()
	posts, gets := 0, 0
	id := report.digest()
	s, key := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/owner/repo/issues/1/comments" {
			t.Fatal("recovery used another issue")
		}
		if r.Method == http.MethodPost {
			posts++
			w.WriteHeader(502)
		} else {
			gets++
			_ = json.NewEncoder(w).Encode([]map[string]any{{
				"html_url": "https://github.com/owner/repo/issues/1#issuecomment-4000000008", "id": int64(4000000008),
				"body": report.issueBody(id), "created_at": time.Now(),
			}})
		}
	})
	body, _ := json.Marshal(report)
	w := httptest.NewRecorder()
	s.ServeHTTP(w, signedRequest(t, s, key, body))
	if w.Code != 502 {
		t.Fatal(w.Code)
	}
	w = httptest.NewRecorder()
	s.ServeHTTP(w, signedRequest(t, s, key, body))
	if w.Code != 200 || posts != 1 || gets != 1 {
		t.Fatalf("pending recovery: %d %s, posts=%d gets=%d", w.Code, w.Body, posts, gets)
	}
}

func TestQuotaAndStorageFailure(t *testing.T) {
	s, key := fixture(t, func(w http.ResponseWriter, r *http.Request) { t.Error("should not reach github") })
	for i := 0; i < 10; i++ {
		s.state.records[fmt.Sprint(i)] = record{CreatedAt: s.now()}
	}
	body, _ := json.Marshal(validReport())
	w := httptest.NewRecorder()
	s.ServeHTTP(w, signedRequest(t, s, key, body))
	if w.Code != 429 {
		t.Fatal(w.Code)
	}
	s.state.records = map[string]record{}
	s.state.path = filepath.Join(t.TempDir(), "missing", "state.json")
	w = httptest.NewRecorder()
	s.ServeHTTP(w, signedRequest(t, s, key, body))
	if w.Code != 503 || len(s.state.records) != 0 {
		t.Fatal("storage failure was not rolled back")
	}
}

func TestFixedCommentResult(t *testing.T) {
	g := githubClient{repository: "owner/repo"}
	valid := issueResult{CommentURL: "https://github.com/owner/repo/issues/1#issuecomment-4000000007",
		CommentID: 4000000007, IssueNumber: 1}
	if !g.validResult(valid) {
		t.Fatal("64-bit comment ID was rejected")
	}
	for _, result := range []issueResult{
		{CommentURL: "https://github.com/owner/repo/issues/9", IssueNumber: 9},
		{CommentURL: valid.CommentURL, CommentID: valid.CommentID, IssueNumber: 2},
		{CommentURL: "https://example.org/", CommentID: valid.CommentID, IssueNumber: 1},
		{CommentURL: valid.CommentURL, CommentID: 0, IssueNumber: 1},
	} {
		if g.validResult(result) {
			t.Fatal("accepted a result outside the support comment")
		}
	}
}

func TestVendorMetadataRedactionAndEscaping(t *testing.T) {
	report := validReport()
	report.BuildIncremental = "serial=private-device-value"
	report.RomVersion = "imei=123456789012345"
	report.MarketingName = "[link](https://example.org)<img src=x>"
	if err := report.validate(); err != nil {
		t.Fatal(err)
	}
	if report.BuildIncremental != "[redacted]" || report.RomVersion != "[redacted]" {
		t.Fatal("vendor metadata bypassed redaction")
	}
	body := report.issueBody(report.digest())
	if !strings.HasPrefix(body, "## <code>[link](https://example.org)&lt;img src=x&gt;</code>") ||
		strings.Contains(body, "private-device-value") || strings.Contains(body, "<img") {
		t.Fatal("device metadata introduced active markup or private identifiers")
	}
}

func TestOnlyTestedCompatibilityResults(t *testing.T) {
	s, key := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("untested report reached GitHub")
		w.WriteHeader(500)
	})
	for _, value := range []string{"", "untested", "unknown"} {
		report := validReport()
		report.Compatibility = value
		body, _ := json.Marshal(report)
		w := httptest.NewRecorder()
		s.ServeHTTP(w, signedRequest(t, s, key, body))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("result %q: got %d, want 400", value, w.Code)
		}
	}
	if len(s.state.records) != 0 {
		t.Fatal("untested report consumed persistent state or quota")
	}
	for _, value := range []string{"working", "not_working"} {
		report := validReport()
		report.Compatibility = value
		if err := report.validate(); err != nil {
			t.Fatalf("tested result %q rejected: %v", value, err)
		}
	}
}
