package ipc

import "github.com/jingyugao/keep-run/internal/task"

type ErrorResponse struct {
	Error string `json:"error"`
}

type CreateTaskRequest struct {
	Name string            `json:"name"`
	Argv []string          `json:"argv"`
	Cwd  string            `json:"cwd"`
	Env  map[string]string `json:"env"`
	Life string            `json:"life"`
}

type StartStopResponse struct {
	Task task.Record `json:"task"`
}

type StartAllResponse struct {
	Tasks []task.Record `json:"tasks"`
}

type ListTasksResponse struct {
	Tasks []task.Record `json:"tasks"`
}

type DaemonStatusResponse struct {
	OK         bool   `json:"ok"`
	Installed  bool   `json:"installed"`
	SocketPath string `json:"socket_path"`
	PID        int    `json:"pid"`
}
