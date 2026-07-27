package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/file", s.handleFileInfo)
	mux.HandleFunc("GET /api/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("POST /api/tasks", s.handleAddTask)
	mux.HandleFunc("PATCH /api/tasks/{id}", s.handleUpdateTask)
	mux.HandleFunc("DELETE /api/tasks/{id}", s.handleDeleteTask)
	mux.HandleFunc("POST /api/tasks/{id}/deps", s.handleAddDep)
	mux.HandleFunc("DELETE /api/tasks/{id}/deps/{parentId}", s.handleRemoveDep)
	mux.HandleFunc("GET /api/ready", s.handleReady)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /bind-vscode", s.handleBindVSCode)

	// Static assets at / and /static/*.
	mux.Handle("GET /", s.assetHandler())

	return mux
}

// --- File info ----------------------------------------------------------

type fileInfo struct {
	Path      string            `json:"path"`
	Format    string            `json:"format"`
	TaskCount int               `json:"task_count"`
	UpdatedAt string            `json:"updated_at,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

func (s *Server) handleFileInfo(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.cfg.Store.Get(nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	path, format := storeLocation(s.cfg.Store)
	// Git mode has no file to stat, so it reports no updated_at; a file
	// backend over an in-memory filesystem has a Location that does not
	// exist on disk either, and the failed stat leaves it empty the same
	// way it always did.
	updated := ""
	if s.cfg.Store.Backend() != meads.BackendGit {
		if fi, err := os.Stat(s.cfg.Store.Location()); err == nil {
			updated = fi.ModTime().UTC().Format(time.RFC3339)
		}
	}
	writeJSON(w, http.StatusOK, fileInfo{
		Path:      path,
		Format:    format,
		TaskCount: len(tasks),
		UpdatedAt: updated,
	})
}

// --- List / Get / Ready -------------------------------------------------

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.cfg.Store.Get(nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if tasks == nil {
		tasks = []meads.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tasks, err := s.cfg.Store.Get([]int{id})
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, tasks[0])
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	tasks, err := s.cfg.Store.Ready()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if tasks == nil {
		tasks = []meads.Task{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

// --- Add ----------------------------------------------------------------

func (s *Server) handleAddTask(w http.ResponseWriter, r *http.Request) {
	var in meads.AddTaskInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		writeError(w, http.StatusBadRequest, errors.New("title is required"))
		return
	}
	t := meads.Task{Title: in.Title}
	status := in.Status
	if status == "" {
		status = "open"
	}
	if err := meads.ValidateStatus(status); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	t.SetStatus(status)
	if in.Priority != "" {
		p, err := meads.NormalizePriority(in.Priority)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		t.SetPriority(p)
	}
	if in.Type != "" {
		if err := meads.ValidateType(in.Type); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		t.SetType(in.Type)
	}
	if in.Description != "" {
		t.Description = in.Description
	}
	id, err := s.cfg.Store.Add(t)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	t.ID = id
	s.events.publish(event{Kind: "task_added", Task: &t})
	writeJSON(w, http.StatusCreated, meads.AddTaskOutput{ID: id})
}

// --- Update -------------------------------------------------------------

func (s *Server) handleUpdateTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var in meads.UpdateTaskInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if in.Status != "" {
		if err := meads.ValidateStatus(in.Status); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	if in.Type != "" {
		if err := meads.ValidateType(in.Type); err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
	}
	priority := in.Priority
	if priority != "" {
		norm, err := meads.NormalizePriority(priority)
		if err != nil {
			writeError(w, http.StatusBadRequest, err)
			return
		}
		priority = norm
	}
	err = s.cfg.Store.Update(id, func(t *meads.Task) {
		if in.Status != "" {
			t.SetStatus(in.Status)
		}
		if priority != "" {
			t.SetPriority(priority)
		}
		if in.Title != "" {
			t.Title = in.Title
		}
		if in.Type != "" {
			t.SetType(in.Type)
		}
		if in.Description != "" {
			t.Description = in.Description
		}
		if in.StatusReason != "" {
			t.StatusReason = in.StatusReason
		}
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tasks, err := s.cfg.Store.Get([]int{id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.events.publish(event{Kind: "task_updated", Task: &tasks[0]})
	writeJSON(w, http.StatusOK, tasks[0])
}

// --- Delete -------------------------------------------------------------

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := s.cfg.Store.Delete(id); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	s.events.publish(event{Kind: "task_deleted", TaskID: id})
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
}

// --- Dependencies -------------------------------------------------------

type addDepInput struct {
	ParentID int `json:"parent_id"`
}

func (s *Server) handleAddDep(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	var in addDepInput
	if err := decodeBody(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if in.ParentID == 0 {
		writeError(w, http.StatusBadRequest, errors.New("parent_id is required"))
		return
	}
	err = s.cfg.Store.Update(id, func(t *meads.Task) {
		t.AddDep(in.ParentID)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tasks, err := s.cfg.Store.Get([]int{id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.events.publish(event{Kind: "task_updated", Task: &tasks[0]})
	writeJSON(w, http.StatusOK, tasks[0])
}

func (s *Server) handleRemoveDep(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	parentRaw := r.PathValue("parentId")
	parent, perr := strconv.Atoi(parentRaw)
	if perr != nil || parent <= 0 {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid parent id %q", parentRaw))
		return
	}
	err = s.cfg.Store.Update(id, func(t *meads.Task) {
		t.RemoveDep(parent)
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	tasks, err := s.cfg.Store.Get([]int{id})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	s.events.publish(event{Kind: "task_updated", Task: &tasks[0]})
	writeJSON(w, http.StatusOK, tasks[0])
}

// --- Helpers ------------------------------------------------------------

func pathID(r *http.Request) (int, error) {
	raw := r.PathValue("id")
	id, err := strconv.Atoi(raw)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid task id %q", raw)
	}
	return id, nil
}

func decodeBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}
