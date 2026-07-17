package server

import (
	"encoding/json"
	"os"
	"time"
)

const (
	wbComponentsStatePath = "/run/olcrtc-wb-components-state.json"
	panelUpdateStatePath  = "/run/olcrtc-panel-update-state.json"
)

type operationFileState struct {
	Phase     string `json:"phase"`
	Message   string `json:"message"`
	Percent   int    `json:"percent"`
	UpdatedAt int64  `json:"updated_at"`
}

type operationProgress struct {
	State      string    `json:"state"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
	Error      string    `json:"error,omitempty"`
	Output     string    `json:"output,omitempty"`
	Phase      string    `json:"phase,omitempty"`
	Message    string    `json:"message,omitempty"`
	Percent    int       `json:"percent"`
	UpdatedAt  int64     `json:"updated_at,omitempty"`
}

func operationProgressFrom(state operationState, path string) operationProgress {
	result := operationProgress{
		State:      state.State,
		StartedAt:  state.StartedAt,
		FinishedAt: state.FinishedAt,
		Error:      state.Error,
		Output:     state.Output,
	}
	var fileState operationFileState
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &fileState) == nil {
		freshForRun := state.StartedAt.IsZero() || fileState.UpdatedAt >= state.StartedAt.Unix()
		if state.State == "" || state.State == "idle" || freshForRun {
			result.Phase = fileState.Phase
			result.Message = fileState.Message
			result.Percent = min(max(fileState.Percent, 0), 100)
			result.UpdatedAt = fileState.UpdatedAt
		}
	}
	if result.State == "" || result.State == "idle" {
		switch result.Phase {
		case "completed":
			result.State = "completed"
		case "error":
			result.State = "failed"
		case "":
			result.State = "idle"
		default:
			result.State = "running"
			if result.UpdatedAt > 0 && time.Since(time.Unix(result.UpdatedAt, 0)) > 35*time.Minute {
				result.State = "failed"
				result.Error = "Операция была прервана или превысила лимит времени"
			}
		}
	}
	if state.State == "running" {
		result.State = "running"
	}
	if state.State == "failed" {
		result.State = "failed"
	}
	if result.Phase == "error" && result.State != "running" {
		result.State = "failed"
		if result.Error == "" {
			result.Error = result.Message
		}
	}
	if result.State == "completed" {
		result.Percent = 100
		if result.Message == "" {
			result.Message = "Операция завершена"
		}
	}
	return result
}
