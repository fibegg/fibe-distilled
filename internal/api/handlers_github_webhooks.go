package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"

	"github.com/fibegg/fibe-distilled/internal/api/response"
	githubapi "github.com/fibegg/fibe-distilled/internal/github"
	"github.com/fibegg/fibe-distilled/internal/worker"
)

// githubWebhookMaxBytes limits a GitHub webhook delivery body to 1 MiB.
const githubWebhookMaxBytes = 1 << 20

// githubWebhook receives manually configured GitHub webhook deliveries.
func (s *Server) githubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, githubWebhookMaxBytes))
	if err != nil {
		response.BadRequest(w, r, "invalid GitHub webhook body")
		return
	}
	if !githubapi.ValidWebhookSignature(s.cfg.GitHubWebhookSecret, r.Header.Get("X-Hub-Signature-256"), body) {
		response.Unauthorized(w, r)
		return
	}
	event, actionable, err := githubapi.PushEventFromWebhook(r.Header.Get("X-Github-Event"), body)
	if err != nil {
		response.BadRequest(w, r, "invalid GitHub webhook JSON")
		return
	}
	response.JSON(w, r, http.StatusOK, map[string]any{"status": "ok"})
	if !actionable {
		return
	}
	ctx := context.WithoutCancel(r.Context())
	go func(ctx context.Context) {
		if _, err := s.worker.HandleGitHubPush(ctx, workerGitHubPushEvent(event)); err != nil {
			slog.Error("github webhook processing failed", "error", err)
		}
	}(ctx)
}

// workerGitHubPushEvent converts GitHub package output to worker input.
func workerGitHubPushEvent(event githubapi.PushEvent) worker.GitHubPushEvent {
	return worker.GitHubPushEvent{
		RepositoryFullName: event.RepositoryFullName,
		Branch:             event.Branch,
		After:              event.After,
	}
}
