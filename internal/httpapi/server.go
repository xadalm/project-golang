package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
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
	"example.com/linkup/internal/postgres"
	"example.com/linkup/internal/profiles"
)

const (
	defaultLimit = 20
	maxLimit     = 50
)

var validID = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,32}$`)

type Server struct {
	profiles      *profiles.Service
	interactions  *interaction.Service
	commandBudget time.Duration
	logger        *slog.Logger
	requestIDs    atomic.Uint64
}

func New(profileRepository profiles.Repository, interactions *interaction.Service, commandBudget time.Duration, logger *slog.Logger) (*Server, error) {
	if profileRepository == nil || interactions == nil || commandBudget <= 0 {
		return nil, errors.New("profile repository, interaction service, and positive command budget are required")
	}
	if logger == nil {
		return nil, errors.New("logger is required")
	}
	return &Server{
		profiles: profiles.NewService(profileRepository), interactions: interactions,
		commandBudget: commandBudget, logger: logger,
	}, nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /v1/me/discovery", s.withActor(http.HandlerFunc(s.discover)))
	mux.Handle("PUT /v1/me/likes/{targetID}", s.withActor(http.HandlerFunc(s.like)))
	mux.Handle("PUT /v1/me/blocks/{targetID}", s.withActor(http.HandlerFunc(s.block)))
	mux.Handle("GET /v1/me/pairs/{targetID}", s.withActor(http.HandlerFunc(s.inspect)))
	mux.Handle("GET /v1/profiles/{profileID}", s.withActor(http.HandlerFunc(s.getProfile)))
	mux.Handle("PUT /v1/profiles/{profileID}", s.withActor(http.HandlerFunc(s.updateProfile)))
	return s.requestLogging(mux)
}

type actorKey struct{}
type requestIDKey struct{}

func (s *Server) withActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actor := r.Header.Get("X-Demo-User")
		if !validID.MatchString(actor) {
			writeError(w, http.StatusBadRequest, "invalid_demo_identity", "X-Demo-User is required and must be a valid user ID")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), s.commandBudget)
		defer cancel()
		if _, err := s.profiles.Get(ctx, actor); errors.Is(err, profiles.ErrNotFound) {
			writeError(w, http.StatusBadRequest, "invalid_demo_identity", "X-Demo-User does not identify a known user")
			return
		} else if err != nil {
			s.writeProfileError(w, r.WithContext(ctx), err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(ctx, actorKey{}, actor)))
	})
}

func routeLabel(r *http.Request) string {
	switch {
	case r.URL.Path == "/healthz":
		return "/healthz"
	case r.URL.Path == "/v1/me/discovery":
		return "/v1/me/discovery"
	case strings.HasPrefix(r.URL.Path, "/v1/me/likes/"):
		return "/v1/me/likes/{targetID}"
	case strings.HasPrefix(r.URL.Path, "/v1/me/blocks/"):
		return "/v1/me/blocks/{targetID}"
	case strings.HasPrefix(r.URL.Path, "/v1/me/pairs/"):
		return "/v1/me/pairs/{targetID}"
	case strings.HasPrefix(r.URL.Path, "/v1/profiles/"):
		return "/v1/profiles/{profileID}"
	default:
		return "unmatched"
	}
}

func (s *Server) discover(w http.ResponseWriter, r *http.Request) {
	if !rejectBody(w, r) {
		return
	}
	query, ok := parseDiscoveryQuery(w, r)
	if !ok {
		return
	}
	actor := r.Context().Value(actorKey{}).(string)
	subjectRecord, err := s.profiles.Get(r.Context(), actor)
	if err != nil {
		s.writeProfileError(w, r, err)
		return
	}
	profileRecords, err := s.profiles.List(r.Context())
	if err != nil {
		s.writeProfileError(w, r, err)
		return
	}
	candidates := make([]discovery.Profile, 0, len(profileRecords))
	profileByID := make(map[string]profiles.Profile, len(profileRecords))
	for _, profile := range profileRecords {
		candidates = append(candidates, toDiscoveryProfile(profile))
		profileByID[profile.ID] = profile
	}
	recommended := discovery.Discover(toDiscoveryProfile(subjectRecord), candidates)
	items := make([]discoveryItem, 0)
	for _, id := range recommended {
		profile := profileByID[id]
		if contains(profile.Interests, query.interest) {
			items = append(items, discoveryItem{ID: id, Interests: append([]string{}, profile.Interests...)})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	start := min(query.offset, len(items))
	end := min(start+query.limit, len(items))
	writeJSON(w, http.StatusOK, discoveryResponse{
		Items: append([]discoveryItem{}, items[start:end]...), Limit: query.limit, Offset: query.offset,
	})
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
	result, err := s.interactions.React(r.Context(), actor, target, interaction.StatusPending)
	if err != nil {
		s.writeInteractionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, interactionResponse(result))
}

func (s *Server) block(w http.ResponseWriter, r *http.Request) {
	if !rejectBody(w, r) {
		return
	}
	target := r.PathValue("targetID")
	if !validID.MatchString(target) {
		writeError(w, http.StatusBadRequest, "invalid_target_id", "Target ID has an invalid format")
		return
	}
	actor := r.Context().Value(actorKey{}).(string)
	result, err := s.interactions.Block(r.Context(), actor, target)
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
	result, err := s.interactions.Inspect(r.Context(), actor, target)
	if err != nil {
		s.writeInteractionError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, interactionResponse(result))
}

func (s *Server) getProfile(w http.ResponseWriter, r *http.Request) {
	if !rejectBody(w, r) {
		return
	}
	profileID := r.PathValue("profileID")
	if !validID.MatchString(profileID) {
		writeError(w, http.StatusBadRequest, "invalid_profile_id", "Profile ID has an invalid format")
		return
	}
	profile, err := s.profiles.Get(r.Context(), profileID)
	if err != nil {
		s.writeProfileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, profileResponse(profile))
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	profileID := r.PathValue("profileID")
	if !validID.MatchString(profileID) {
		writeError(w, http.StatusBadRequest, "invalid_profile_id", "Profile ID has an invalid format")
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var request struct {
		ExpectedVersion *int64   `json:"expected_version"`
		Interests       []string `json:"interests"`
	}
	if err := decoder.Decode(&request); err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "Profile body exceeds 4096 bytes")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", "Profile body must be one valid JSON object")
		}
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "Profile body must contain exactly one JSON value")
		return
	}
	if request.ExpectedVersion == nil || request.Interests == nil {
		writeError(w, http.StatusBadRequest, "invalid_profile", "expected_version and interests are required")
		return
	}
	actor := r.Context().Value(actorKey{}).(string)
	profile, err := s.profiles.Update(r.Context(), actor, profileID, *request.ExpectedVersion, request.Interests)
	if err != nil {
		s.writeProfileError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, profileResponse(profile))
}

func (s *Server) writeInteractionError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(r.Context().Err(), context.Canceled) {
		return
	}
	switch {
	case errors.Is(err, interaction.ErrInvalidParticipant):
		writeError(w, http.StatusBadRequest, "invalid_participants", "Source and target must be different valid users")
	case errors.Is(err, interaction.ErrParticipantNotFound):
		writeError(w, http.StatusNotFound, "target_not_found", "A required participant was not found")
	case errors.Is(err, interaction.ErrParticipantInactive):
		writeError(w, http.StatusForbidden, "participant_inactive", "An inactive participant cannot interact")
	case errors.Is(err, interaction.ErrPairBlocked):
		writeError(w, http.StatusConflict, "user_blocked", "Interaction is disabled for this pair")
	case errors.Is(err, interaction.ErrInvalidAction):
		writeError(w, http.StatusConflict, "invalid_transition", "The requested transition is not allowed")
	case errors.Is(err, postgres.ErrDatabaseTimeout):
		writeError(w, http.StatusServiceUnavailable, "database_timeout", "The database operation exceeded its time budget")
	case errors.Is(err, context.DeadlineExceeded):
		s.logger.Error("interaction dependency timed out", "request_id", requestID(r), "error", err)
		writeError(w, http.StatusGatewayTimeout, "dependency_timeout", "The required dependency timed out")
	case errors.Is(err, interaction.ErrDependencyFailure):
		s.logger.Error("interaction dependency failed", "request_id", requestID(r), "error", err)
		writeError(w, http.StatusBadGateway, "dependency_failure", "The required dependency failed")
	case errors.Is(err, postgres.ErrCommitUnconfirmed):
		s.logError("database commit outcome unconfirmed", r, err)
		writeError(w, http.StatusServiceUnavailable, "commit_unconfirmed", "The result of the database commit is unknown; inspect state before retrying")
	case errors.Is(err, postgres.ErrTransactionRetryable):
		writeError(w, http.StatusServiceUnavailable, "transaction_retryable", "The database could not complete the transaction; retry after reading current state")
	case errors.Is(err, postgres.ErrDatabaseFailure):
		s.logError("database request failed", r, err)
		writeError(w, http.StatusInternalServerError, "database_failure", "An internal database error occurred")
	default:
		s.logError("interaction request failed", r, err)
		writeError(w, http.StatusInternalServerError, "internal_error", "An internal error occurred")
	}
}

func (s *Server) writeProfileError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(r.Context().Err(), context.Canceled) {
		return
	}
	switch {
	case errors.Is(err, profiles.ErrNotFound):
		writeError(w, http.StatusNotFound, "profile_not_found", "Profile was not found")
	case errors.Is(err, profiles.ErrForbidden):
		writeError(w, http.StatusForbidden, "profile_forbidden", "Only the profile owner may edit it")
	case errors.Is(err, profiles.ErrVersionConflict):
		writeError(w, http.StatusConflict, "version_conflict", "Profile changed; read the current version before editing again")
	case errors.Is(err, profiles.ErrInvalidProfile):
		writeError(w, http.StatusBadRequest, "invalid_profile", "Profile fields are invalid")
	case errors.Is(err, postgres.ErrCommitUnconfirmed):
		s.logError("profile commit outcome unconfirmed", r, err)
		writeError(w, http.StatusServiceUnavailable, "commit_unconfirmed", "The result of the database commit is unknown; read the profile before retrying")
	case errors.Is(err, postgres.ErrTransactionRetryable):
		writeError(w, http.StatusServiceUnavailable, "transaction_retryable", "The database could not complete the transaction; retry after reading current state")
	case errors.Is(err, postgres.ErrDatabaseTimeout):
		writeError(w, http.StatusServiceUnavailable, "database_timeout", "The database operation exceeded its time budget")
	case errors.Is(err, postgres.ErrDatabaseFailure):
		s.logError("profile database request failed", r, err)
		writeError(w, http.StatusInternalServerError, "database_failure", "An internal database error occurred")
	case errors.Is(err, context.DeadlineExceeded):
		writeError(w, http.StatusServiceUnavailable, "database_timeout", "The database operation exceeded its time budget")
	default:
		s.logError("profile request failed", r, err)
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
	Blocked    bool   `json:"blocked"`
	Changed    bool   `json:"changed"`
}

type profileResponseDTO struct {
	ID        string   `json:"id"`
	Active    bool     `json:"active"`
	Interests []string `json:"interests"`
	Version   int64    `json:"version"`
}

func interactionResponse(result interaction.Result) pairResponse {
	return pairResponse{
		UserID: result.Reaction.SourceID, TargetID: result.Reaction.TargetID,
		Status: string(result.Reaction.Status), Matched: result.Matched,
		MatchedNow: result.MatchedNow, Blocked: result.Blocked, Changed: result.Changed,
	}
}

func profileResponse(profile profiles.Profile) profileResponseDTO {
	return profileResponseDTO{
		ID: profile.ID, Active: profile.Active, Interests: append([]string{}, profile.Interests...),
		Version: profile.Version,
	}
}

func toDiscoveryProfile(profile profiles.Profile) discovery.Profile {
	return discovery.Profile{ID: profile.ID, Active: profile.Active, Interests: append([]string{}, profile.Interests...)}
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

func requestID(r *http.Request) any {
	return r.Context().Value(requestIDKey{})
}

func (s *Server) logError(message string, r *http.Request, err error) {
	s.logger.Error(message, "request_id", requestID(r), "error", err)
}
