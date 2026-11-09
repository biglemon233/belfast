// 覆盖固定入口更新、身份防冒用、单次配对与离线租约。
// Covers stable URL updates, device ownership, single-use pairing and offline leases.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const testID = "0123456789abcdef0123456789abcdef"
const testKey = "0123456789012345678901234567890123456789012345678901234567890123"

func fixture(t *testing.T) *service {
	t.Helper()
	s, err := openService(filepath.Join(t.TempDir(), "registry.db"), "https://registry.example")
	if err != nil {
		t.Fatal(err)
	}
	s.probe = func(context.Context, string) bool { return true }
	t.Cleanup(func() { s.db.Close() })
	return s
}
func call(s *service, method, path, body, key string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.1:1234"
	if key != "" {
		r.Header.Set("Authorization", "Bearer "+key)
	}
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	return w
}
func register(t *testing.T, s *service, target string) {
	t.Helper()
	w := call(s, "POST", "/api/v1/devices/register", fmt.Sprintf(`{"device_id":%q,"name":"AP test","tunnel_url":%q}`, testID, target), testKey)
	if w.Code != 200 {
		t.Fatalf("register: %d %s", w.Code, w.Body.String())
	}
}
func TestStableRedirectUpdateAndOwnership(t *testing.T) {
	s := fixture(t)
	register(t, s, "https://first.trycloudflare.com")
	wrong := call(s, "POST", "/api/v1/devices/register", fmt.Sprintf(`{"device_id":%q,"name":"takeover","tunnel_url":"https://attacker.trycloudflare.com"}`, testID), strings.Repeat("x", 64))
	if wrong.Code != 403 {
		t.Fatal(wrong.Code)
	}
	register(t, s, "https://second.trycloudflare.com")
	w := call(s, "GET", "/d/"+testID+"/api/v1/ws?example=value", "", "")
	if w.Code != 301 || w.Header().Get("Location") != "https://second.trycloudflare.com/api/v1/ws?example=value" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal(w.Code, w.Header())
	}
	call(s, "DELETE", "/api/v1/devices/"+testID, "", testKey)
	if call(s, "GET", "/d/"+testID+"/", "", "").Code != 302 {
		t.Fatal("offline entry must redirect to the error page")
	}
}
func TestRejectArbitraryTunnelTargets(t *testing.T) {
	for _, target := range []string{"http://a.trycloudflare.com", "https://trycloudflare.com.evil.test", "https://localhost", "https://a.trycloudflare.com/path", "https://a.trycloudflare.com?token=x", "https://a.trycloudflare.com:443", "https://user@a.trycloudflare.com"} {
		t.Run(target, func(t *testing.T) {
			s := fixture(t)
			w := call(s, "POST", "/api/v1/devices/register", fmt.Sprintf(`{"device_id":%q,"name":"AP","tunnel_url":%q}`, testID, target), testKey)
			if w.Code != 400 {
				t.Fatal(w.Code)
			}
		})
	}
}
func makePair(t *testing.T, s *service) map[string]any {
	t.Helper()
	register(t, s, "https://first.trycloudflare.com")
	w := call(s, "POST", "/api/v1/pairings", fmt.Sprintf(`{"device_id":%q,"access_token":%q}`, testID, strings.Repeat("a", 43)), testKey)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p map[string]any
	json.Unmarshal(w.Body.Bytes(), &p)
	if !codePattern.MatchString(p["code"].(string)) {
		t.Fatal(p)
	}
	return p
}
func TestPairingIsAtomicSingleUse(t *testing.T) {
	s := fixture(t)
	p := makePair(t, s)
	var wg sync.WaitGroup
	results := make(chan int, 2)
	for range 2 {
		wg.Go(func() {
			w := call(s, "POST", "/api/v1/pairings/claim", fmt.Sprintf(`{"code":%q}`, p["code"]), "")
			results <- w.Code
		})
	}
	wg.Wait()
	close(results)
	counts := map[int]int{}
	for status := range results {
		counts[status]++
	}
	if counts[200] != 1 || counts[404] != 1 {
		t.Fatal(counts)
	}
	var token string
	s.db.QueryRow("SELECT token FROM pairings").Scan(&token)
	if token != "" {
		t.Fatal("claimed token was retained")
	}
	status := call(s, "GET", "/api/v1/pairings/"+p["id"].(string), "", testKey)
	if !strings.Contains(status.Body.String(), "claimed") {
		t.Fatal(status.Body.String())
	}
}
func TestPairExpiryAndRateLimit(t *testing.T) {
	s := fixture(t)
	p := makePair(t, s)
	s.db.Exec("UPDATE pairings SET expires=0")
	for range 5 {
		w := call(s, "POST", "/api/v1/pairings/claim", fmt.Sprintf(`{"code":%q}`, p["code"]), "")
		if w.Code != 404 {
			t.Fatal(w.Code)
		}
	}
	if call(s, "POST", "/api/v1/pairings/claim", `{"code":"000000"}`, "").Code != 429 {
		t.Fatal("missing rate limit")
	}
}
func TestScanSecretAndPersistentMapping(t *testing.T) {
	s := fixture(t)
	p := makePair(t, s)
	w := call(s, "POST", "/api/v1/pairings/claim", fmt.Sprintf(`{"code":%q,"scan_secret":"wrong"}`, p["code"]), "")
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
	secret := p["particle_secret"].(string)
	if !opticalSecretPattern.MatchString(secret) {
		t.Fatal("optical secret must contain 128 bits")
	}
	w = call(s, "POST", "/api/v1/pairings/claim", fmt.Sprintf(`{"scan_secret":%q}`, secret), "")
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	var result map[string]string
	json.Unmarshal(w.Body.Bytes(), &result)
	if result["access_token"] != strings.Repeat("a", 43) {
		t.Fatal("credential missing")
	}
}

func TestLeaseExpiryAndDatabaseRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "persistent.db")
	s, err := openService(path, "https://registry.example")
	if err != nil {
		t.Fatal(err)
	}
	register(t, s, "https://first.trycloudflare.com")
	s.db.Close()
	s, err = openService(path, "https://registry.example")
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	s.probe = func(context.Context, string) bool { return true }
	if call(s, "GET", "/d/"+testID+"/", "", "").Code != 301 {
		t.Fatal("mapping lost after restart")
	}
	s.db.Exec("UPDATE devices SET seen=seen-181")
	if call(s, "GET", "/d/"+testID+"/", "", "").Code != 302 {
		t.Fatal("expired lease must redirect to the error page")
	}
	register(t, s, "https://restarted.trycloudflare.com")
	w := call(s, "GET", "/d/"+testID+"/", "", "")
	if w.Header().Get("Location") != "https://restarted.trycloudflare.com/" {
		t.Fatal(w.Header())
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPortalAndOfflineRedirects(t *testing.T) {
	s := fixture(t)
	for _, path := range []string{"/", "/error", "/assets/style.css", "/api/healthz"} {
		w := call(s, "GET", path, "", "")
		if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("%s: %d", path, w.Code)
		}
	}
	for _, path := range []string{"/d", "/d/unknown-device-id/"} {
		w := call(s, "GET", path, "", "")
		if w.Code != 302 || w.Header().Get("Location") != "/error" {
			t.Fatal(w.Code, w.Header())
		}
	}
	register(t, s, "https://dead.trycloudflare.com")
	s.probe = func(context.Context, string) bool { return false }
	w := call(s, "GET", "/d/"+testID+"/", "", "")
	if w.Code != 302 || w.Header().Get("Location") != "/error" {
		t.Fatal("dead tunnel was not redirected", w.Code)
	}
	if call(s, "POST", "/v1/devices/register", "{}", testKey).Code != 404 {
		t.Fatal("registration must use /api")
	}
}

func TestTunnelProbeChecksAPAndNeverFollowsRedirects(t *testing.T) {
	for _, sample := range []struct {
		code  int
		body  string
		alive bool
	}{
		{200, `{"status":"ok","protocolVersion":1}`, true},
		{200, `{"status":"ok"}`, false},
		{200, `<html>error</html>`, false},
		{530, `{"status":"ok","protocolVersion":1}`, false},
		{502, "unavailable", false},
		{301, "redirect", false},
	} {
		client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://probe.trycloudflare.com/healthz" || r.Header.Get("Authorization") != "" {
				t.Fatal(r.URL, r.Header)
			}
			return &http.Response{StatusCode: sample.code, Body: io.NopCloser(strings.NewReader(sample.body)), Header: http.Header{}}, nil
		})}
		if got := tunnelProbe(client)(context.Background(), "https://probe.trycloudflare.com"); got != sample.alive {
			t.Fatalf("%d %s: %v", sample.code, sample.body, got)
		}
	}
}
