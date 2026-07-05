package storage

import (
	"database/sql"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// buildRecordSelectSQL returns the shared BuildRecord SELECT column order.
func buildRecordSelectSQL() string {
	return `SELECT id,playground_id,prop_id,service_name,branch,commit_sha,status,image_ref,build_dockerfile_path,build_target,build_args_digest,build_identity_digest,build_platform,build_cache_key,reused,logs,error_message,started_at,completed_at,created_at,updated_at FROM build_records`
}

// scanBuildRecord decodes one BuildRecord row.
func scanBuildRecord(row scanner) (domain.BuildRecord, error) {
	var record domain.BuildRecord
	var playgroundID, propID sql.NullInt64
	var errMsg, started, completed sql.NullString
	var created, updated string
	var reused int
	err := row.Scan(&record.ID, &playgroundID, &propID, &record.ServiceName, &record.Branch, &record.CommitSHA, &record.Status, &record.ImageRef, &record.BuildDockerfilePath, &record.BuildTarget, &record.BuildArgsDigest, &record.BuildIdentityDigest, &record.BuildPlatform, &record.BuildCacheKey, &reused, &record.Logs, &errMsg, &started, &completed, &created, &updated)
	if err != nil {
		return record, err
	}
	record.PlaygroundID = int64Ptr(playgroundID)
	record.PropID = int64Ptr(propID)
	record.Reused = reused == 1
	record.ErrorMessage = stringPtr(errMsg)
	if err := assignNullableStoredTime("build_records.started_at", started, &record.StartedAt); err != nil {
		return record, err
	}
	if err := assignNullableStoredTime("build_records.completed_at", completed, &record.CompletedAt); err != nil {
		return record, err
	}
	if record.CreatedAt, err = parseStoredTime("build_records.created_at", created); err != nil {
		return record, err
	}
	if record.UpdatedAt, err = parseStoredTime("build_records.updated_at", updated); err != nil {
		return record, err
	}
	return record, nil
}
