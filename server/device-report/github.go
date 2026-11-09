// 将已验证报告追加到固定 GitHub Issue，并查回不确定响应对应的评论。
//
// Appends verified reports to a fixed GitHub issue and reconciles uncertain comments.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type githubClient struct {
	client                     *http.Client
	baseURL, repository, token string
}

type issueResult struct {
	CommentURL  string `json:"commentUrl"`
	CommentID   int64  `json:"commentId"`
	IssueNumber int    `json:"issueNumber"`
	Duplicate   bool   `json:"duplicate"`
}

func (g githubClient) request(ctx context.Context, method, path string, body []byte, output any) error {
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	req.Header.Set("User-Agent", "AzurPilot-Device-Report/1")
	res, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("github request failed")
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("github status %d", res.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(output)
}

func (g githubClient) validResult(result issueResult) bool {
	return result.IssueNumber == 1 && result.CommentID > 0 &&
		result.CommentURL == "https://github.com/"+g.repository+"/issues/1#issuecomment-"+strconv.FormatInt(result.CommentID, 10)
}

func (g githubClient) create(ctx context.Context, report Report, id string) (issueResult, error) {
	body, _ := json.Marshal(map[string]string{"body": report.issueBody(id)})
	var comment struct {
		URL string `json:"html_url"`
		ID  int64  `json:"id"`
	}
	err := g.request(ctx, http.MethodPost, "/repos/"+g.repository+"/issues/1/comments", body, &comment)
	result := issueResult{CommentURL: comment.URL, CommentID: comment.ID, IssueNumber: 1}
	if err == nil && !g.validResult(result) {
		err = fmt.Errorf("invalid github response")
	}
	return result, err
}

func (g githubClient) find(ctx context.Context, id string, since time.Time) (issueResult, bool, error) {
	marker := "<!-- azurpilot-device-report:" + id + " -->"
	for page := 1; page <= 10; page++ {
		var comments []struct {
			URL  string `json:"html_url"`
			ID   int64  `json:"id"`
			Body string `json:"body"`
		}
		query := url.Values{"since": {since.Add(-time.Minute).UTC().Format(time.RFC3339)},
			"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		if err := g.request(ctx, http.MethodGet, "/repos/"+g.repository+"/issues/1/comments?"+query.Encode(), nil, &comments); err != nil {
			return issueResult{}, false, err
		}
		for _, comment := range comments {
			result := issueResult{CommentURL: comment.URL, CommentID: comment.ID, IssueNumber: 1, Duplicate: true}
			if strings.Contains(comment.Body, marker) && g.validResult(result) {
				return result, true, nil
			}
		}
		if len(comments) < 100 {
			return issueResult{}, false, nil
		}
	}
	return issueResult{}, false, fmt.Errorf("github reconciliation limit")
}
