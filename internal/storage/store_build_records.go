package storage

import (
	"context"
	"time"

	"github.com/fibegg/fibe-distilled/internal/domain"
)

// ReusableBuildRecordQuery describes a prior image build that can satisfy a new deploy.
type ReusableBuildRecordQuery struct {
	// PropID scopes cross-playground reuse to a known repository Prop.
	PropID *int64
	// PlaygroundID scopes fallback reuse when no repository Prop exists.
	PlaygroundID int64
	// ServiceName scopes fallback reuse when no repository Prop exists.
	ServiceName string
	// Branch is the selected source branch.
	Branch string
	// CommitSHA is the source commit that must match the image metadata.
	CommitSHA string
	// BuildIdentityDigest is the dockerfile/target/build-args digest.
	BuildIdentityDigest string
	// BuildPlatform is the optional Docker platform used by the build.
	BuildPlatform string
}

// CreateBuildRecord inserts a dynamic image build record.
func (s *DB) CreateBuildRecord(ctx context.Context, record domain.BuildRecord) (domain.BuildRecord, error) {
	now := time.Now().UTC()
	record.CreatedAt = now
	record.UpdatedAt = now
	if record.Status == "" {
		record.Status = domain.BuildStatusPending
	}
	res, err := s.db.ExecContext(ctx, `INSERT INTO build_records (playground_id,prop_id,service_name,branch,commit_sha,status,image_ref,build_dockerfile_path,build_target,build_args_digest,build_identity_digest,build_platform,build_cache_key,reused,logs,error_message,started_at,completed_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		nullableInt64(record.PlaygroundID), nullableInt64(record.PropID), record.ServiceName, record.Branch, record.CommitSHA, record.Status, record.ImageRef, record.BuildDockerfilePath, record.BuildTarget, record.BuildArgsDigest, record.BuildIdentityDigest, record.BuildPlatform, record.BuildCacheKey, boolToInt(record.Reused), record.Logs, nullableString(record.ErrorMessage), nullableTime(record.StartedAt), nullableTime(record.CompletedAt), encodeTime(now), encodeTime(now))
	if err != nil {
		return record, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return record, err
	}
	record.ID = id
	return record, nil
}

// SaveBuildRecord updates an existing build record.
func (s *DB) SaveBuildRecord(ctx context.Context, record domain.BuildRecord) (domain.BuildRecord, error) {
	record.UpdatedAt = time.Now().UTC()
	res, err := s.db.ExecContext(ctx, `UPDATE build_records SET playground_id=?,prop_id=?,service_name=?,branch=?,commit_sha=?,status=?,image_ref=?,build_dockerfile_path=?,build_target=?,build_args_digest=?,build_identity_digest=?,build_platform=?,build_cache_key=?,reused=?,logs=?,error_message=?,started_at=?,completed_at=?,updated_at=? WHERE id=?`,
		nullableInt64(record.PlaygroundID), nullableInt64(record.PropID), record.ServiceName, record.Branch, record.CommitSHA, record.Status, record.ImageRef, record.BuildDockerfilePath, record.BuildTarget, record.BuildArgsDigest, record.BuildIdentityDigest, record.BuildPlatform, record.BuildCacheKey, boolToInt(record.Reused), record.Logs, nullableString(record.ErrorMessage), nullableTime(record.StartedAt), nullableTime(record.CompletedAt), encodeTime(record.UpdatedAt), record.ID)
	return record, requireRowsAffected(res, err)
}

// GetBuildRecord fetches one BuildRecord by ID.
func (s *DB) GetBuildRecord(ctx context.Context, id int64) (domain.BuildRecord, error) {
	return queryOne(ctx, s.db, buildRecordSelectSQL()+` WHERE id=?`, id, scanBuildRecord)
}

// ListReusableBuildRecords returns successful or in-flight records matching a build identity.
func (s *DB) ListReusableBuildRecords(ctx context.Context, query ReusableBuildRecordQuery) ([]domain.BuildRecord, error) {
	if query.PropID != nil {
		return queryRows(ctx, s.db, buildRecordSelectSQL()+` WHERE prop_id=? AND branch=? AND commit_sha=? AND build_identity_digest=? AND build_platform=? AND status IN (?,?) ORDER BY id DESC`, scanBuildRecord,
			*query.PropID, query.Branch, query.CommitSHA, query.BuildIdentityDigest, query.BuildPlatform, domain.BuildStatusSuccess, domain.BuildStatusBuilding)
	}
	return queryRows(ctx, s.db, buildRecordSelectSQL()+` WHERE prop_id IS NULL AND playground_id=? AND service_name=? AND branch=? AND commit_sha=? AND build_identity_digest=? AND build_platform=? AND status IN (?,?) ORDER BY id DESC`, scanBuildRecord,
		query.PlaygroundID, query.ServiceName, query.Branch, query.CommitSHA, query.BuildIdentityDigest, query.BuildPlatform, domain.BuildStatusSuccess, domain.BuildStatusBuilding)
}
