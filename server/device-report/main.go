// 提供小内存机型提交网关，验证客户端签名并串行追加 GitHub 评论。
//
// Provides a low-memory reporting gateway, verifying client signatures and serializing comments.
package main

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var noncePattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type service struct {
	cert   *x509.Certificate
	github githubClient
	state  *store
	gate   chan struct{}
	nonces map[string]time.Time
	now    func() time.Time
}

func loadClientCert(path string) (*x509.Certificate, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(body)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("invalid client certificate")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}
	if _, ok := cert.PublicKey.(*rsa.PublicKey); !ok {
		return nil, errors.New("RSA certificate required")
	}
	clientAuth := false
	for _, usage := range cert.ExtKeyUsage {
		if usage == x509.ExtKeyUsageClientAuth {
			clientAuth = true
		}
	}
	if !clientAuth {
		return nil, errors.New("clientAuth usage required")
	}
	return cert, nil
}

func (s *service) authenticate(r *http.Request, body []byte) bool {
	now := s.now()
	timestamp := r.Header.Get("X-Report-Timestamp")
	ts, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || strconv.FormatInt(ts, 10) != timestamp {
		return false
	}
	when := time.Unix(ts, 0)
	if when.Before(now.Add(-5*time.Minute)) || when.After(now.Add(5*time.Minute)) ||
		now.Before(s.cert.NotBefore) || now.After(s.cert.NotAfter) {
		return false
	}
	nonce := r.Header.Get("X-Report-Nonce")
	if !noncePattern.MatchString(nonce) {
		return false
	}
	for key, expiry := range s.nonces {
		if !expiry.After(now) {
			delete(s.nonces, key)
		}
	}
	if _, used := s.nonces[nonce]; used || len(s.nonces) >= 2048 {
		return false
	}
	signature, err := base64.StdEncoding.DecodeString(r.Header.Get("X-Report-Signature"))
	if err != nil {
		return false
	}
	message := append([]byte("POST\n/v1/device-reports\n"+timestamp+"\n"+nonce+"\n"), body...)
	digest := sha256.Sum256(message)
	if rsa.VerifyPKCS1v15(s.cert.PublicKey.(*rsa.PublicKey), crypto.SHA256, digest[:], signature) != nil {
		return false
	}
	s.nonces[nonce] = now.Add(10 * time.Minute)
	return true
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func fail(w http.ResponseWriter, status int, code string) {
	respond(w, status, map[string]string{"error": code})
}

func (s *service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" && r.Method == http.MethodGet {
		respond(w, 200, map[string]string{"status": "ok"})
		return
	}
	if r.URL.Path != "/v1/device-reports" {
		fail(w, 404, "not_found")
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		fail(w, 405, "method_not_allowed")
		return
	}
	if r.Header.Get("Content-Type") != "application/json" {
		fail(w, 415, "invalid_content_type")
		return
	}
	// 单个请求独占状态，限制低配服务器的峰值内存及 GitHub 并发。
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	default:
		w.Header().Set("Retry-After", "5")
		fail(w, 503, "busy")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8192))
	if err != nil {
		fail(w, 413, "payload_too_large")
		return
	}
	if !s.authenticate(r, body) {
		fail(w, 401, "unauthorized")
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var report Report
	if err := decoder.Decode(&report); err != nil {
		fail(w, 400, "invalid_report")
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(w, 400, "invalid_report")
		return
	}
	if err := report.validate(); err != nil {
		fail(w, 400, "invalid_report")
		return
	}
	id := report.digest()
	entry, exists := s.state.records[id]
	if exists && entry.Result != nil && s.github.validResult(*entry.Result) {
		result := *entry.Result
		result.Duplicate = true
		respond(w, 200, result)
		return
	}
	if !exists {
		if !s.state.withinQuota(s.now()) {
			w.Header().Set("Retry-After", "3600")
			fail(w, 429, "rate_limited")
			return
		}
		entry = record{CreatedAt: s.now()}
		if err := s.state.put(id, entry); err != nil {
			fail(w, 503, "storage_unavailable")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if exists {
		result, found, err := s.github.find(ctx, id, entry.CreatedAt)
		if err != nil {
			fail(w, 502, "github_unavailable")
			return
		}
		if found {
			entry.Result = &result
			if err := s.state.put(id, entry); err != nil {
				fail(w, 503, "storage_unavailable")
				return
			}
			respond(w, 200, result)
			return
		}
		// 等待 GitHub 将不确定的创建结果暴露出来，避免断线后马上再发一个。
		if s.now().Sub(entry.CreatedAt) < 2*time.Minute {
			w.Header().Set("Retry-After", "120")
			fail(w, 503, "pending")
			return
		}
	}
	result, err := s.github.create(ctx, report, id)
	if err != nil {
		log.Printf("comment creation: %v", err)
		fail(w, 502, "github_unavailable")
		return
	}
	entry.Result = &result
	if err := s.state.put(id, entry); err != nil {
		fail(w, 503, "storage_unavailable")
		return
	}
	respond(w, 201, result)
}

func main() {
	certPath := os.Getenv("CLIENT_CERT_FILE")
	if certPath == "" {
		certPath = "/etc/azurpilot-device-report/client-cert.pem"
	}
	cert, err := loadClientCert(certPath)
	if err != nil {
		log.Fatal(err)
	}
	repository, token := os.Getenv("GITHUB_REPOSITORY"), os.Getenv("GITHUB_TOKEN")
	if !repositoryPattern.MatchString(repository) || strings.TrimSpace(token) == "" {
		log.Fatal("missing github configuration")
	}
	statePath := os.Getenv("STATE_FILE")
	if statePath == "" {
		statePath = "/var/lib/azurpilot-device-report/state.json"
	}
	state, err := openStore(statePath)
	if err != nil {
		log.Fatal(err)
	}
	addr := os.Getenv("LISTEN_ADDR")
	if addr == "" {
		addr = "127.0.0.1:18882"
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		log.Fatal("listen address must be loopback")
	}
	client := &http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	handler := &service{cert: cert, github: githubClient{client: client, baseURL: "https://api.github.com", repository: repository, token: token},
		state: state, gate: make(chan struct{}, 1), nonces: map[string]time.Time{}, now: time.Now}
	server := &http.Server{Addr: addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout: 10 * time.Second, WriteTimeout: 35 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	log.Print("device-report service listening")
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(fmt.Errorf("serve: %w", err))
	}
}
