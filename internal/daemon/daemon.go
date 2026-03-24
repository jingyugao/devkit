package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/jingyugao/keep-run/internal/config"
	"github.com/jingyugao/keep-run/internal/durationutil"
	"github.com/jingyugao/keep-run/internal/ipc"
	"github.com/jingyugao/keep-run/internal/paths"
	"github.com/jingyugao/keep-run/internal/storage"
	"github.com/jingyugao/keep-run/internal/task"
)

type Server struct {
	cfg         config.Config
	store       *storage.Store
	httpServer  *http.Server
	listener    net.Listener
	stopTimeout time.Duration

	mu      sync.Mutex
	runners map[string]*runner
}

type runner struct {
	cmd        *exec.Cmd
	logWriter  *lineLogWriter
	done       chan struct{}
	manualStop bool
	reason     string
}

type lineLogWriter struct {
	mu   sync.Mutex
	file *os.File
}

func New(cfg config.Config) (*Server, error) {
	if err := paths.EnsureBaseDirs(); err != nil {
		return nil, err
	}
	stopTimeout, err := durationutil.Parse(cfg.Defaults.StopTimeout)
	if err != nil {
		return nil, fmt.Errorf("parse stop timeout: %w", err)
	}
	if err := os.Remove(paths.SocketPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", paths.SocketPath())
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(paths.SocketPath(), 0o600); err != nil {
		ln.Close()
		return nil, err
	}

	s := &Server{
		cfg:         cfg,
		store:       storage.New(),
		listener:    ln,
		stopTimeout: stopTimeout,
		runners:     make(map[string]*runner),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/tasks", s.handleTasks)
	mux.HandleFunc("/tasks/", s.handleTask)
	s.httpServer = &http.Server{Handler: mux}
	return s, nil
}

func (s *Server) Run(ctx context.Context) error {
	if err := os.WriteFile(paths.PIDFile(), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
		return err
	}
	defer os.Remove(paths.PIDFile())

	if err := s.reconcileStartup(); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		_ = s.httpServer.Shutdown(context.Background())
	}()
	go s.expiryLoop(ctx)

	err := s.httpServer.Serve(s.listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ipc.DaemonStatusResponse{
		OK:         true,
		Installed:  false,
		SocketPath: paths.SocketPath(),
		PID:        os.Getpid(),
	})
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.handleListTasks(w, r)
	case http.MethodPost:
		s.handleCreateTask(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleTask(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/tasks/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusBadRequest, "missing task reference")
		return
	}
	ref := parts[0]
	if len(parts) == 1 {
		if r.Method == http.MethodDelete {
			s.handleDeleteTask(w, r, ref)
			return
		}
		http.NotFound(w, r)
		return
	}
	switch parts[1] {
	case "start":
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		s.handleStartTask(w, r, ref)
	case "stop":
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		s.handleStopTask(w, r, ref)
	case "logs":
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		s.handleLogs(w, r, ref)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	records, err := s.store.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	runningOnly := r.URL.Query().Get("state") == "running"
	now := time.Now()
	result := make([]task.Record, 0, len(records))
	for _, record := range records {
		if record.IsExpired(now) && record.State.RuntimeState != task.StateExpired {
			record.State.RuntimeState = task.StateExpired
		}
		if runningOnly && record.State.RuntimeState != task.StateRunning {
			continue
		}
		result = append(result, record)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Spec.ID < result[j].Spec.ID
	})
	writeJSON(w, http.StatusOK, ipc.ListTasksResponse{Tasks: result})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var req ipc.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid create payload")
		return
	}
	record, err := s.createTask(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, ipc.StartStopResponse{Task: record})
}

func (s *Server) handleStartTask(w http.ResponseWriter, r *http.Request, ref string) {
	record, err := s.startByRef(ref)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ipc.StartStopResponse{Task: record})
}

func (s *Server) handleStopTask(w http.ResponseWriter, r *http.Request, ref string) {
	record, err := s.stopByRef(ref)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, ipc.StartStopResponse{Task: record})
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request, ref string) {
	force := r.URL.Query().Get("force") == "1"
	if err := s.deleteByRef(ref, force); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request, ref string) {
	record, err := s.store.Resolve(ref)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	lines := s.cfg.Logs.TailLines
	if raw := r.URL.Query().Get("lines"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			lines = parsed
		}
	}
	follow := r.URL.Query().Get("follow") == "1"
	logPath := paths.LogFile(record.Spec.ID)

	content, err := tailLines(logPath, lines)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if len(content) > 0 {
		_, _ = w.Write(content)
	}
	if !follow {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
	followFile(r.Context(), w, logPath, flusher)
}

func (s *Server) createTask(req ipc.CreateTaskRequest) (task.Record, error) {
	if len(req.Argv) == 0 {
		return task.Record{}, fmt.Errorf("missing command")
	}
	if req.Name != "" {
		if err := s.ensureUniqueName(req.Name, ""); err != nil {
			return task.Record{}, err
		}
	}
	createdAt := time.Now()
	var expiresAt *time.Time
	if req.Life != "" {
		life, err := durationutil.Parse(req.Life)
		if err != nil {
			return task.Record{}, err
		}
		if life > 0 {
			t := createdAt.Add(life)
			expiresAt = &t
		}
	}

	record := task.Record{
		Spec: task.Spec{
			ID:              newTaskID(),
			Name:            req.Name,
			Argv:            append([]string(nil), req.Argv...),
			Cwd:             req.Cwd,
			Env:             cloneEnv(req.Env),
			Life:            req.Life,
			ExpiresAt:       expiresAt,
			RunAfterRestart: req.RunAfterRestart,
			CreatedAt:       createdAt,
		},
		State: task.State{
			DesiredState: task.DesiredRunning,
			RuntimeState: task.StateStopped,
			Reason:       "registered",
		},
	}
	if err := s.store.Save(record); err != nil {
		return task.Record{}, err
	}
	return s.startRecord(record)
}

func (s *Server) startByRef(ref string) (task.Record, error) {
	record, err := s.store.Resolve(ref)
	if err != nil {
		return task.Record{}, err
	}
	if record.Spec.Name != "" {
		if err := s.ensureUniqueName(record.Spec.Name, record.Spec.ID); err != nil {
			return task.Record{}, err
		}
	}
	return s.startRecord(record)
}

func (s *Server) startRecord(record task.Record) (task.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	if record.IsExpired(now) {
		record.State.DesiredState = task.DesiredStopped
		record.State.RuntimeState = task.StateExpired
		record.State.Reason = "task life expired"
		record.State.StoppedAt = ptrTime(now)
		record.State.PID = 0
		record.State.ExitCode = nil
		if err := s.store.Save(record); err != nil {
			return task.Record{}, err
		}
		return record, fmt.Errorf("task %s is expired", record.Spec.ID)
	}
	if existing := s.runners[record.Spec.ID]; existing != nil {
		return record, fmt.Errorf("task %s is already running", record.Spec.ID)
	}

	logFile, err := os.OpenFile(paths.LogFile(record.Spec.ID), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return task.Record{}, err
	}
	cmd := exec.Command(record.Spec.Argv[0], record.Spec.Argv[1:]...)
	cmd.Dir = record.Spec.Cwd
	cmd.Env = envMapToSlice(record.Spec.Env)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		logFile.Close()
		return task.Record{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		logFile.Close()
		return task.Record{}, err
	}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return task.Record{}, err
	}

	now = time.Now()
	record.State.DesiredState = task.DesiredRunning
	record.State.RuntimeState = task.StateRunning
	record.State.PID = cmd.Process.Pid
	record.State.StartedAt = &now
	record.State.StoppedAt = nil
	record.State.ExitCode = nil
	record.State.Reason = "started"
	if err := s.store.Save(record); err != nil {
		logFile.Close()
		return task.Record{}, err
	}

	r := &runner{
		cmd:       cmd,
		logWriter: &lineLogWriter{file: logFile},
		done:      make(chan struct{}),
	}
	s.runners[record.Spec.ID] = r
	go s.captureOutput(record.Spec.ID, r.logWriter, "stdout", stdout)
	go s.captureOutput(record.Spec.ID, r.logWriter, "stderr", stderr)
	go s.waitOnProcess(record.Spec.ID, r)
	return record, nil
}

func (s *Server) stopByRef(ref string) (task.Record, error) {
	record, err := s.store.Resolve(ref)
	if err != nil {
		return task.Record{}, err
	}

	s.mu.Lock()
	r := s.runners[record.Spec.ID]
	if r == nil {
		now := time.Now()
		record.State.DesiredState = task.DesiredStopped
		record.State.RuntimeState = task.StateStopped
		record.State.Reason = "already stopped"
		record.State.StoppedAt = &now
		record.State.PID = 0
		if err := s.store.Save(record); err != nil {
			s.mu.Unlock()
			return task.Record{}, err
		}
		s.mu.Unlock()
		return record, nil
	}
	r.manualStop = true
	r.reason = "stopped by user"
	record.State.DesiredState = task.DesiredStopped
	record.State.Reason = "stopping"
	if err := s.store.Save(record); err != nil {
		s.mu.Unlock()
		return task.Record{}, err
	}
	s.mu.Unlock()

	if err := stopProcessGroup(record.State.PID, s.stopTimeout); err != nil {
		return task.Record{}, err
	}
	<-r.done
	return s.store.Resolve(record.Spec.ID)
}

func (s *Server) deleteByRef(ref string, force bool) error {
	record, err := s.store.Resolve(ref)
	if err != nil {
		return err
	}
	s.mu.Lock()
	r := s.runners[record.Spec.ID]
	s.mu.Unlock()
	if r != nil {
		if !force {
			return fmt.Errorf("task %s is running; use --force to stop and remove it", record.Spec.ID)
		}
		if _, err := s.stopByRef(record.Spec.ID); err != nil {
			return err
		}
	}
	return s.store.Delete(record.Spec.ID)
}

func (s *Server) captureOutput(taskID string, writer *lineLogWriter, stream string, src io.ReadCloser) {
	defer src.Close()
	scanner := bufio.NewScanner(src)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		writer.WriteLine(stream, scanner.Text())
	}
}

func (s *Server) waitOnProcess(taskID string, r *runner) {
	defer close(r.done)
	err := r.cmd.Wait()
	r.logWriter.Close()

	record, loadErr := s.store.Load(taskID)
	if loadErr != nil {
		s.mu.Lock()
		delete(s.runners, taskID)
		s.mu.Unlock()
		return
	}

	now := time.Now()
	record.State.PID = 0
	record.State.StoppedAt = &now
	record.State.DesiredState = task.DesiredStopped
	if record.IsExpired(now) {
		record.State.RuntimeState = task.StateExpired
		record.State.Reason = "task life expired"
		record.State.ExitCode = nil
	} else if r.manualStop {
		record.State.RuntimeState = task.StateStopped
		record.State.Reason = r.reason
		record.State.ExitCode = nil
	} else {
		exitCode := exitCodeFrom(err)
		record.State.ExitCode = &exitCode
		if exitCode == 0 {
			record.State.RuntimeState = task.StateExited
			record.State.Reason = "process exited"
		} else {
			record.State.RuntimeState = task.StateFailed
			record.State.Reason = fmt.Sprintf("process exited with code %d", exitCode)
		}
	}
	_ = s.store.Save(record)

	s.mu.Lock()
	delete(s.runners, taskID)
	s.mu.Unlock()
}

func (s *Server) reconcileStartup() error {
	records, err := s.store.List()
	if err != nil {
		return err
	}
	now := time.Now()
	for _, record := range records {
		changed := false
		if record.State.RuntimeState == task.StateRunning || record.State.RuntimeState == task.StateStarting {
			if record.State.PID > 0 {
				_ = stopProcessGroup(record.State.PID, 2*time.Second)
			}
			record.State.PID = 0
			record.State.StoppedAt = &now
			record.State.RuntimeState = task.StateStopped
			record.State.Reason = "daemon restarted"
			changed = true
		}
		if record.IsExpired(now) {
			record.State.DesiredState = task.DesiredStopped
			record.State.RuntimeState = task.StateExpired
			record.State.Reason = "task life expired"
			record.State.StoppedAt = &now
			changed = true
		}
		if changed {
			if err := s.store.Save(record); err != nil {
				return err
			}
		}
	}

	for _, record := range records {
		if record.State.DesiredState != task.DesiredRunning || !record.Spec.RunAfterRestart || record.IsExpired(now) {
			continue
		}
		if _, err := s.startRecord(record); err != nil {
			continue
		}
	}
	return nil
}

func (s *Server) expiryLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.enforceExpiry()
		}
	}
}

func (s *Server) enforceExpiry() {
	records, err := s.store.List()
	if err != nil {
		return
	}
	now := time.Now()
	for _, record := range records {
		if !record.IsExpired(now) || record.State.RuntimeState == task.StateExpired {
			continue
		}
		if record.State.RuntimeState == task.StateRunning {
			_, _ = s.stopByRef(record.Spec.ID)
		}
		record.State.DesiredState = task.DesiredStopped
		record.State.RuntimeState = task.StateExpired
		record.State.Reason = "task life expired"
		record.State.StoppedAt = &now
		record.State.PID = 0
		_ = s.store.Save(record)
	}
}

func (s *Server) ensureUniqueName(name string, excludeID string) error {
	records, err := s.store.List()
	if err != nil {
		return err
	}
	for _, record := range records {
		if record.Spec.ID == excludeID {
			continue
		}
		if record.Spec.Name == name {
			return fmt.Errorf("task name %q already exists", name)
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, ipc.ErrorResponse{Error: message})
}

func newTaskID() string {
	return ulid.Make().String()
}

func cloneEnv(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func envMapToSlice(in map[string]string) []string {
	if len(in) == 0 {
		return []string{}
	}
	keys := make([]string, 0, len(in))
	for key := range in {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+in[key])
	}
	return out
}

func exitCodeFrom(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode()
	}
	return -1
}

func ptrTime(t time.Time) *time.Time {
	return &t
}

func stopProcessGroup(pid int, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	if err := syscall.Kill(-pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func processExists(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

func (w *lineLogWriter) WriteLine(stream string, line string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ts := time.Now().Format(time.RFC3339)
	_, _ = fmt.Fprintf(w.file, "%s %s %s\n", ts, stream, line)
}

func (w *lineLogWriter) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	_ = w.file.Close()
}

func tailLines(path string, lines int) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if lines <= 0 {
		return data, nil
	}
	segments := strings.Split(string(data), "\n")
	if len(segments) == 0 {
		return data, nil
	}
	start := 0
	if len(segments) > lines+1 {
		start = len(segments) - lines - 1
	}
	return []byte(strings.Join(segments[start:], "\n")), nil
}

func followFile(ctx context.Context, w io.Writer, path string, flusher http.Flusher) {
	var offset int64
	if info, err := os.Stat(path); err == nil {
		offset = info.Size()
	}
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			file, err := os.Open(path)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				return
			}
			info, err := file.Stat()
			if err != nil {
				file.Close()
				return
			}
			if info.Size() < offset {
				offset = 0
			}
			if _, err := file.Seek(offset, io.SeekStart); err != nil {
				file.Close()
				return
			}
			if _, err := io.Copy(w, file); err == nil {
				offset = info.Size()
				flusher.Flush()
			}
			file.Close()
		}
	}
}
