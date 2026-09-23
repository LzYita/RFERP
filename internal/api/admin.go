package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"app/internal/model"
	"app/internal/paths"
	"app/internal/usecase"
)

func (s *Server) handleListUsers(w http.ResponseWriter, _ *http.Request) {
	users, err := s.apps.ListUsers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, users)
}

func (s *Server) handleUserCount(w http.ResponseWriter, _ *http.Request) {
	n, err := s.apps.UserCount()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"count": n})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in usecase.CreateUserInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	u, err := s.apps.CreateUser(in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var in usecase.UpdateUserInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.UpdateUser(in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleResetPassword(w http.ResponseWriter, r *http.Request) {
	var in usecase.ResetPasswordInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.ResetPassword(in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var in usecase.ChangePasswordInput
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if err := s.apps.ChangePassword(in); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Query().Get("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.apps.DeleteUser(id); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleListProducts(w http.ResponseWriter, _ *http.Request) {
	list, err := s.apps.ListProducts()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateProduct(w http.ResponseWriter, r *http.Request) {
	var p model.Product
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	out, err := s.apps.CreateProduct(&p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleBackupDatabase(w http.ResponseWriter, _ *http.Request) {
	path, err := s.apps.BackupDatabase(paths.BackupDir())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"path": path})
}

func (s *Server) handleExportAll(w http.ResponseWriter, _ *http.Request) {
	files, dir, err := s.apps.ExportAllDataCSV(paths.ExportDir())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "dir": dir})
}
