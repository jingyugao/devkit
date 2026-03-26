package task

import "time"

type DesiredState string

const (
	DesiredRunning DesiredState = "running"
	DesiredStopped DesiredState = "stopped"
)

type RuntimeState string

const (
	StateStarting RuntimeState = "starting"
	StateRunning  RuntimeState = "running"
	StateStopped  RuntimeState = "stopped"
	StateExited   RuntimeState = "exited"
	StateFailed   RuntimeState = "failed"
	StateExpired  RuntimeState = "expired"
)

type Spec struct {
	ID        string            `json:"id"`
	Name      string            `json:"name,omitempty"`
	Argv      []string          `json:"argv"`
	Cwd       string            `json:"cwd"`
	Env       map[string]string `json:"env,omitempty"`
	Life      string            `json:"life,omitempty"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
	CreatedAt time.Time         `json:"created_at"`
}

type State struct {
	DesiredState DesiredState `json:"desired_state"`
	RuntimeState RuntimeState `json:"runtime_state"`
	PID          int          `json:"pid,omitempty"`
	StartedAt    *time.Time   `json:"started_at,omitempty"`
	StoppedAt    *time.Time   `json:"stopped_at,omitempty"`
	ExitCode     *int         `json:"exit_code,omitempty"`
	RestartCount int          `json:"restart_count,omitempty"`
	Reason       string       `json:"reason,omitempty"`
}

type Record struct {
	Spec  Spec  `json:"spec"`
	State State `json:"state"`
}

func (r Record) IsExpired(now time.Time) bool {
	return r.Spec.ExpiresAt != nil && !now.Before(*r.Spec.ExpiresAt)
}

func (r Record) Identifier() string {
	if r.Spec.Name != "" {
		return r.Spec.Name
	}
	return r.Spec.ID
}

func (r Record) DisplayID() string {
	return r.Spec.ID
}
