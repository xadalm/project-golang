package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"example.com/linkup/internal/discovery"
	"example.com/linkup/internal/interaction"
)

const (
	defaultLimit = 20
	maxLimit     = 50
)

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

type ProfileCatalog interface {
	Get(id string) (discovery.Profile, error)
	List() ([]discovery.Profile, error)
}

type Server struct {
	catalog       ProfileCatalog
	interactions  *interaction.Service
	commandBudget time.Duration
	logger        *slog.Logger
	requestIDs    atomic.Uint64
}

func New(catalog ProfileCatalog, interactions *interaction.Service, commandBudget time.Duration, logger *slog.Logger) (*Server, error) {
	if catalog == nil || interactions == nil || commandBudget <= 0 {
		return nil, errors.New("catalog, interaction service, and positive command budget are required")
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	return &Server{catalog: catalog, interactions: interactions, commandBudget: commandBudget, logger: logger}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /v1/me/discovery", s.withActor(http.HandlerFunc(s.discover)))
	mux.Handle("PUT /v1/me/likes/{targetID}", s.withActor(http.HandlerFunc(s.like)))
	mux.Handle("GET /v1/me/pairs/{targetID}", s.withActor(http.HandlerFunc(s.inspect)))
	return s.requestLogging(mux)
}

func (s *Server) withActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-Demo-User")
		if !validID.MatchString(actor) {
			writeError(w, http.StatusBadRequest, "invalid_demo_identity", "X-Demo-User is required and must be a valid user ID")
			return
		}
		if _, err := s.catalog.Get(actor); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_demo_identity", "X-Demo-User does not identify a known user")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), actorKey{}, actor)))
	})
}

type requestIDKey struct{}

func routeLabel(r *http.Request) string {
	switch {
	case r.URL.Path == "/healthz":
		return "/healthz"
	case r.URL.Path == "/v1/me/discovery":
		return "/v1/me/discovery"
	case strings.HasPrefix(r.URL.Path, "/v1/me/likes/"):
		return "/v1/me/likes/{targetID}"
	case strings.HasPrefix(r.URL.Path, "/v1/me/pairs/"):
		return "/v1/me/pairs/{targetID}"
	default:
		return "unmatched"
	}
}

type actorKey struct{}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	if !rejectBody(w, r) {
		return
	}
	query, ok := parseDiscoveryQuery(w, r)
	if !ok {
		return
	}
	actor := r.Context().Value(actorKey{}).(string)
	subject, err := s.catalog.Get(actor)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Unable to load current user")
		return
	}
	profiles, err := s.catalog.List()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "Unable to load discovery profiles")
		return
	}
	recommended := discovery.Discover(subject, profiles)
	profileByID := make(map[string]discovery.Profile, len(profiles))
	for _, profile := range profiles {
		profileByID[profile.ID] = profile
	}
	items := make([]discoveryItem, 0)
	for _, id := range recommended {
		profile := profileByID[id]
		if contains(profile.Interests, query.interest) {
			items = append(items, discoveryItem{ID: id, Interests: append([]string{}, profile.Interests...)})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := query.offset
	if start > len(items) {
		start = len(items)
	}
	end := start + query.limit
	if end > len(items) {
		end = len(items)
	}
	page := append([]discoveryItem{}, items[start:end]...)
	writeJSON(w, http.StatusOK, discoveryResponse{Items: page, Limit: query.limit, Offset: query.offset})
}

func (s *Server) like(w http.ResponseWriter, r *http.Request) {
	if !rejectBody(w, r) {
		return
	}
	target := r.PathValue("targetID")
	if !validID.MatchString(target) {
		writeError(w, http.StatusBadRequest, "invalid_target_id", "Target ID has an invalid format")
		return
	}
	actor := r.Context().Value(actorKey{}).(string)
	ctx, cancel := context.WithTimeout(r.Context(), s.commandBudget)
	defer cancel()
	result, err := s.interactions.React(ctx, actor, target, interaction.StatusPending)
	if err != nil {
		s.writeInteractionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, interactionResponse(result))
}

func (s *Server) inspect(w http.ResponseWriter, r *http.Request) {
	if !rejectBody(w, r) {
		return
	}
	target := r.PathValue("targetID")
	if !validID.MatchString(target) {
		writeError(w, http.StatusBadRequest, "invalid_target_id", "Target ID has an invalid format")
		return
	}
	actor := r.Context().Value(actorKey{}).(string)
	ctx, cancel := context.WithTimeout(r.Context(), s.commandBudget)
	defer cancel()
	result, err := s.interactions.Inspect(ctx, actor, target)
	if err != nil {
		s.writeInteractionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, interactionResponse(result))
}

func (s *Server) writeInteractionError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	switch {
	case errors.Is(err, interaction.ErrInvalidParticipant):
		writeError(w, http.StatusBadRequest, "invalid_participants", "Source and target must be different valid users")
	case errors.Is(err, interaction.ErrParticipantNotFound):
		writeError(w, http.StatusNotFound, "target_not_found", "A required participant was not found")
	case errors.Is(err, interaction.ErrParticipantInactive):
		writeError(w, http.StatusForbidden, "participant_inactive", "An inactive participant cannot interact")
	case errors.Is(err, interaction.ErrInvalidAction):
		writeError(w, http.StatusConflict, "invalid_transition", "The requested transition is not allowed")
	case errors.Is(err, context.DeadlineExceeded):
		s.logger.Error("interaction dependency timed out", "request_id", r.Context().Value(requestIDKey{}), "error", err)
		writeError(w, http.StatusGatewayTimeout, "dependency_timeout", "The required dependency timed out")
	case errors.Is(err, interaction.ErrDependencyFailure):
		s.logger.Error("interaction dependency failed", "request_id", r.Context().Value(requestIDKey{}), "error", err)
		writeError(w, http.StatusBadGateway, "dependency_failure", "The required dependency failed")
	default:
		s.logger.Error("interaction request failed", "request_id", r.Context().Value(requestIDKey{}), "error", err)
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
	}
}

func (s *Server) requestLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := fmt.Sprintf("req-%016x", s.requestIDs.Add(1))
		w.Header().Set("X-Request-ID", id)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		next.ServeHTTP(recorder, r.WithContext(ctx))
		s.logger.Info("http request",
			"request_id", id,
			"method", r.Method,
			"route", routeLabel(r),
			"status", recorder.status,
			"duration", time.Since(started),
		)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (w *statusRecorder) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func parseDiscoveryQuery(w http.ResponseWriter, r *http.Request) (discoveryQuery, bool) {
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", "Query string is malformed")
		return discoveryQuery{}, false
	}
	allowed := map[string]bool{"interest": true, "limit": true, "offset": true}
	for key, entries := range values {
		if !allowed[key] || len(entries) != 1 {
			writeError(w, http.StatusBadRequest, "invalid_query", "Unknown or repeated query parameter")
			return discoveryQuery{}, false
		}
	}
	interest := strings.TrimSpace(values.Get("interest"))
	if interest == "" || len(interest) > 64 {
		writeError(w, http.StatusBadRequest, "invalid_interest", "interest is required and must be at most 64 characters")
		return discoveryQuery{}, false
	}
	limit := defaultLimit
	offset := 0
	if value, ok := values["limit"]; ok {
		limit, err = strconv.Atoi(value[0])
		if err != nil || limit < 1 || limit > maxLimit {
			writeError(w, http.StatusBadRequest, "invalid_limit", "limit must be an integer from 1 to 50")
			return discoveryQuery{}, false
		}
	}
	if value, ok := values["offset"]; ok {
		offset, err = strconv.Atoi(value[0])
		if err != nil || offset < 0 {
			writeError(w, http.StatusBadRequest, "invalid_offset", "offset must be a non-negative integer")
			return discoveryQuery{}, false
		}
	}
	return discoveryQuery{interest: interest, limit: limit, offset: offset}, true
}

type discoveryQuery struct {
	interest string
	limit    int
	offset   int
}

type discoveryItem struct {
	ID        string   `json:"id"`
	Interests []string `json:"interests"`
}

type discoveryResponse struct {
	Items  []discoveryItem `json:"items"`
	Limit  int             `json:"limit"`
	Offset int             `json:"offset"`
}

type pairResponse struct {
	UserID     string `json:"user_id"`
	TargetID   string `json:"target_id"`
	Status     string `json:"status"`
	Matched    bool   `json:"matched"`
	MatchedNow bool   `json:"matched_now"`
	Changed    bool   `json:"changed"`
}

func interactionResponse(result interaction.Result) pairResponse {
	return pairResponse{
		UserID: result.Reaction.SourceID, TargetID: result.Reaction.TargetID,
		Status: string(result.Reaction.Status), Matched: result.Matched,
		MatchedNow: result.MatchedNow, Changed: result.Changed,
	}
}

type errorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errorResponse{Code: code, Message: message})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func rejectBody(w http.ResponseWriter, r *http.Request) bool {
	if r.Body == nil {
		return true
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1)
	data, err := io.ReadAll(r.Body)
	if err != nil || len(data) != 0 {
		writeError(w, http.StatusBadRequest, "body_not_allowed", "This route does not accept a request body")
		return false
	}
	return true
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
