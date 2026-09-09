package media

import (
	"time"

	mediadomain "github.com/chenyme/grok2api/backend/internal/domain/media"
)

type mediaAssetDTO struct {
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	MimeType  string `json:"mimeType"`
	SizeBytes int64  `json:"sizeBytes"`
	SHA256    string `json:"sha256"`
	CreatedAt string `json:"createdAt"`
	URL       string `json:"url"`
}

type imageStatsDTO struct {
	TotalImages int64 `json:"totalImages"`
	TotalBytes  int64 `json:"totalBytes"`
}

type mediaJobDTO struct {
	ID            string  `json:"id"`
	Model         string  `json:"model"`
	Prompt        string  `json:"prompt"`
	Status        string  `json:"status"`
	Progress      int     `json:"progress"`
	Seconds       int     `json:"seconds"`
	Size          string  `json:"size"`
	Quality       string  `json:"quality"`
	AccountName   string  `json:"accountName"`
	ClientKeyName string  `json:"clientKeyName"`
	CreatedAt     string  `json:"createdAt"`
	CompletedAt   *string `json:"completedAt"`
	ErrorMessage  string  `json:"errorMessage"`
	AssetID       string  `json:"assetId"`
}

type videoStatsDTO struct {
	TotalJobs  int64 `json:"totalJobs"`
	Completed  int64 `json:"completed"`
	Failed     int64 `json:"failed"`
	InProgress int64 `json:"inProgress"`
	Queued     int64 `json:"queued"`
}

type videoJobDetailDTO struct {
	mediaJobDTO
	DiagnosticsEnabled bool                         `json:"diagnosticsEnabled"`
	RequestID          string                       `json:"requestId"`
	Provider           string                       `json:"provider"`
	UpstreamModel      string                       `json:"upstreamModel"`
	AccountID          uint64                       `json:"accountId"`
	EgressNodeName     string                       `json:"egressNodeName"`
	EgressMode         string                       `json:"egressMode"`
	ErrorCode          string                       `json:"errorCode"`
	UpdatedAt          time.Time                    `json:"updatedAt"`
	LeaseUntil         *time.Time                   `json:"leaseUntil"`
	ServerTime         time.Time                    `json:"serverTime"`
	Diagnostics        mediadomain.VideoDiagnostics `json:"diagnostics"`
}

func toMediaJobDTO(j mediadomain.Job) mediaJobDTO {
	var completedAt *string
	assetID := ""
	if j.CompletedAt != nil {
		formatted := j.CompletedAt.UTC().Format(time.RFC3339)
		completedAt = &formatted
	}
	if j.Status == mediadomain.StatusCompleted {
		assetID = j.ResultAssetID
	}
	return mediaJobDTO{
		ID: j.ID, Model: j.Model, Prompt: j.Prompt, Status: string(j.Status),
		Progress: j.Progress, Seconds: j.Seconds, Size: j.Size, Quality: j.Quality,
		AccountName: j.AccountName, ClientKeyName: j.ClientKeyName,
		CreatedAt:   j.CreatedAt.UTC().Format(time.RFC3339),
		CompletedAt: completedAt, ErrorMessage: j.ErrorMessage, AssetID: assetID,
	}
}
