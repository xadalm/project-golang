package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/linkup/internal/demo"
	"example.com/linkup/internal/interaction"
	"example.com/linkup/internal/memory"
)

type participantMap map[string]interaction.Participant

func (p participantMap) GetParticipant(_ context.Context, id string) (interaction.Participant, error) {
	participant, ok := p[id]
	if !ok {
		return interaction.Participant{}, interaction.ErrParticipantNotFound
	}
	return participant, nil
}

func newTestServer(t *testing.T) (*Server, *memory.InteractionRepository) {
	t.Helper()
	profiles := memory.NewProfiles(demo.Profiles())
	repository := memory.NewInteractions()
	participants := participantMap{
		"u1": {ID: "u1", Active: true},
		"u2": {ID: "u2", Active: true},
		"u3": {ID: "u3", Active: false},
		"u4": {ID: "u4", Active: true},
		"u5": {ID: "u5", Active: true},
		"u6": {ID: "u6", Active: true},
	}
	service := interaction.NewService(repository, participants)
	api, err := New(profiles, service, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return api, repository
}

func request(t *testing.T, handler http.Handler, method, path, actor string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if actor != "" {
		req.Header.Set("X-Demo-User", actor)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	return response
}

func TestDiscoveryFiltersThenPaginatesAndReturnsEmptyArray(t *testing.T) {
	api, _ := newTestServer(t)
	handler := api.Handler()
	first := request(t, handler, http.MethodGet, "/v1/me/discovery?interest=go&limit=2&offset=0", "u1")
	if first.Code != http.StatusOK {
		t.Fatalf("first page status %d: %s", first.Code, first.Body)
	}
	var page discoveryResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "u2" || page.Items[1].ID != "u5" {
		t.Fatalf("unexpected first filtered page: %#v", page)
	}
	second := request(t, handler, http.MethodGet, "/v1/me/discovery?interest=go&limit=2&offset=2", "u1")
	if err := json.Unmarshal(second.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if second.Code != http.StatusOK || len(page.Items) != 1 || page.Items[0].ID != "u6" {
		t.Fatalf("unexpected second page: status=%d response=%#v", second.Code, page)
	}
	empty := request(t, handler, http.MethodGet, "/v1/me/discovery?interest=not-a-real-interest&limit=2", "u1")
	if err := json.Unmarshal(empty.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if empty.Code != http.StatusOK || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("empty page must contain items: [], got status=%d body=%s", empty.Code, empty.Body)
	}
}

func TestLikeRepeatOppositeReactionAndInspection(t *testing.T) {
	api, _ := newTestServer(t)
	handler := api.Handler()
	initial := request(t, handler, http.MethodGet, "/v1/me/pairs/u2", "u1")
	var initialPair pairResponse
	if err := json.Unmarshal(initial.Body.Bytes(), &initialPair); err != nil {
		t.Fatal(err)
	}
	if initial.Code != http.StatusOK || initialPair.Status != string(interaction.StatusNone) || initialPair.Matched {
		t.Fatalf("unexpected absent pair result: %s", initial.Body)
	}
	first := request(t, handler, http.MethodPut, "/v1/me/likes/u2", "u1")
	repeat := request(t, handler, http.MethodPut, "/v1/me/likes/u2", "u1")
	var firstResult, repeatResult pairResponse
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(repeat.Body.Bytes(), &repeatResult); err != nil {
		t.Fatal(err)
	}
	if first.Code != http.StatusOK || !firstResult.Changed || repeat.Code != http.StatusOK || repeatResult.Changed {
		t.Fatalf("first=%s repeat=%s", first.Body, repeat.Body)
	}

	reciprocal := request(t, handler, http.MethodPut, "/v1/me/likes/u1", "u2")
	var reciprocalResult pairResponse
	if err := json.Unmarshal(reciprocal.Body.Bytes(), &reciprocalResult); err != nil {
		t.Fatal(err)
	}
	if !reciprocalResult.MatchedNow || !reciprocalResult.Matched {
		t.Fatalf("reciprocal reaction did not create match: %s", reciprocal.Body)
	}
	inspect := request(t, handler, http.MethodGet, "/v1/me/pairs/u2", "u1")
	var pair pairResponse
	if err := json.Unmarshal(inspect.Body.Bytes(), &pair); err != nil {
		t.Fatal(err)
	}
	if inspect.Code != http.StatusOK || !pair.Matched || pair.Changed {
		t.Fatalf("unexpected pair inspection: %s", inspect.Body)
	}
}

func TestInvalidInputAndDependencyRefusalDoNotChangePair(t *testing.T) {
	api, repository := newTestServer(t)
	handler := api.Handler()
	for _, path := range []string{
		"/v1/me/discovery?interest=go&interest=art",
		"/v1/me/discovery?interest=go&limit=51",
		"/v1/me/discovery?interest=go&offset=-1",
		"/v1/me/discovery?interest=go&extra=yes",
		"/v1/me/discovery?interest=%zz",
	} {
		response := request(t, handler, http.MethodGet, path, "u1")
		if response.Code != http.StatusBadRequest {
			t.Errorf("%s returned %d, want 400", path, response.Code)
		}
	}
	missingIdentity := request(t, handler, http.MethodGet, "/v1/me/discovery?interest=go", "")
	if missingIdentity.Code != http.StatusBadRequest {
		t.Fatalf("missing identity returned %d", missingIdentity.Code)
	}

	unknown := request(t, handler, http.MethodPut, "/v1/me/likes/u9", "u1")
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown profile returned %d: %s", unknown.Code, unknown.Body)
	}
	inactive := request(t, handler, http.MethodPut, "/v1/me/likes/u3", "u1")
	if inactive.Code != http.StatusForbidden {
		t.Fatalf("inactive profile returned %d: %s", inactive.Code, inactive.Body)
	}
	first, second, err := repository.GetPair(context.Background(), "u1", "u9")
	if err != nil || first.Status != interaction.StatusNone || second.Status != interaction.StatusNone {
		t.Fatalf("failed command changed state: %#v, %#v, %v", first, second, err)
	}
}

func TestLikeRejectsBodyBeforeCallingDependency(t *testing.T) {
	api, repository := newTestServer(t)
	req := httptest.NewRequest(http.MethodPut, "/v1/me/likes/u2", strings.NewReader(`{"target":"u3"}`))
	req.Header.Set("X-Demo-User", "u1")
	response := httptest.NewRecorder()
	api.Handler().ServeHTTP(response, req)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", response.Code)
	}
	_, err := repository.Get(context.Background(), "u1", "u2")
	if !errors.Is(err, interaction.ErrInteractionNotFound) {
		t.Fatalf("unexpected state after invalid request: %v", err)
	}
}

func TestHealthHasRequestIDWithoutDemoIdentity(t *testing.T) {
	api, _ := newTestServer(t)
	response := request(t, api.Handler(), http.MethodGet, "/healthz", "")
	if response.Code != http.StatusOK || response.Header().Get("X-Request-ID") == "" {
		t.Fatalf("health response %d lacks request ID: %s", response.Code, response.Body)
	}
}

func TestProfileEditUsesOwnerAndExpectedVersion(t *testing.T) {
	api, _ := newTestServer(t)
	handler := api.Handler()
	initial := request(t, handler, http.MethodGet, "/v1/profiles/u1", "u1")
	var profile profileResponseDTO
	if err := json.Unmarshal(initial.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if initial.Code != http.StatusOK || profile.Version != 1 {
		t.Fatalf("initial profile: %d %s", initial.Code, initial.Body)
	}

	update := func(actor, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPut, "/v1/profiles/u1", strings.NewReader(body))
		req.Header.Set("X-Demo-User", actor)
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, req)
		return response
	}
	first := update("u1", `{"expected_version":1,"interests":["go","distributed-systems"]}`)
	if first.Code != http.StatusOK {
		t.Fatalf("profile update: %d %s", first.Code, first.Body)
	}
	if err := json.Unmarshal(first.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.Version != 2 || len(profile.Interests) != 2 {
		t.Fatalf("updated profile: %#v", profile)
	}
	stale := update("u1", `{"expected_version":1,"interests":["stale"]}`)
	if stale.Code != http.StatusConflict || !strings.Contains(stale.Body.String(), "version_conflict") {
		t.Fatalf("stale edit: %d %s", stale.Code, stale.Body)
	}
	forbidden := update("u2", `{"expected_version":2,"interests":["unauthorized"]}`)
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("foreign edit: %d %s", forbidden.Code, forbidden.Body)
	}
	missing := httptest.NewRequest(http.MethodPut, "/v1/profiles/u9", strings.NewReader(`{"expected_version":1,"interests":[]}`))
	missing.Header.Set("X-Demo-User", "u1")
	missing.Header.Set("Content-Type", "application/json")
	missingResponse := httptest.NewRecorder()
	handler.ServeHTTP(missingResponse, missing)
	if missingResponse.Code != http.StatusNotFound {
		t.Fatalf("missing profile update: %d %s", missingResponse.Code, missingResponse.Body)
	}
	check := request(t, handler, http.MethodGet, "/v1/profiles/u1", "u1")
	if err := json.Unmarshal(check.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if check.Code != http.StatusOK || profile.Version != 2 || profile.Interests[1] != "distributed-systems" {
		t.Fatalf("profile changed after failed edits: %d %#v", check.Code, profile)
	}
}

func TestBlockRouteDeactivatesPairAndPreventsNewLike(t *testing.T) {
	api, _ := newTestServer(t)
	handler := api.Handler()
	request(t, handler, http.MethodPut, "/v1/me/likes/u2", "u1")
	request(t, handler, http.MethodPut, "/v1/me/likes/u1", "u2")
	block := request(t, handler, http.MethodPut, "/v1/me/blocks/u2", "u1")
	var blocked pairResponse
	if err := json.Unmarshal(block.Body.Bytes(), &blocked); err != nil {
		t.Fatal(err)
	}
	if block.Code != http.StatusOK || !blocked.Blocked || blocked.Matched {
		t.Fatalf("block result: %d %s", block.Code, block.Body)
	}
	like := request(t, handler, http.MethodPut, "/v1/me/likes/u1", "u2")
	if like.Code != http.StatusConflict || !strings.Contains(like.Body.String(), "user_blocked") {
		t.Fatalf("like after block: %d %s", like.Code, like.Body)
	}
}

func TestDependencyFailuresMapToGatewayStatuses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"upstream failure", fmt.Errorf("%w: status 500", interaction.ErrDependencyFailure), http.StatusBadGateway},
		{"upstream timeout", fmt.Errorf("%w: %w", interaction.ErrDependencyFailure, context.DeadlineExceeded), http.StatusGatewayTimeout},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profiles := memory.NewProfiles(demo.Profiles())
			repository := memory.NewInteractions()
			service := interaction.NewService(repository, participantFailure{err: tt.err})
			api, err := New(profiles, service, time.Second, slog.New(slog.NewTextHandler(io.Discard, nil)))
			if err != nil {
				t.Fatal(err)
			}
			response := request(t, api.Handler(), http.MethodPut, "/v1/me/likes/u2", "u1")
			if response.Code != tt.want {
				t.Fatalf("got %d %s, want %d", response.Code, response.Body, tt.want)
			}
			first, second, err := repository.GetPair(context.Background(), "u1", "u2")
			if err != nil || first.Status != interaction.StatusNone || second.Status != interaction.StatusNone {
				t.Fatalf("dependency failure changed pair: %#v %#v %v", first, second, err)
			}
		})
	}
}

type participantFailure struct {
	err error
}

func (p participantFailure) GetParticipant(context.Context, string) (interaction.Participant, error) {
	return interaction.Participant{}, p.err
}
