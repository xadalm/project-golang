package profileapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"example.com/linkup/internal/interaction"
)

func TestClientGetsParticipantAndHandlesDependencyFailures(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		wantActive bool
		wantErr    error
	}{
		{"valid participant", http.StatusOK, `{"id":"u1","active":true}`, true, nil},
		{"not found", http.StatusNotFound, "missing", false, interaction.ErrParticipantNotFound},
		{"server error", http.StatusInternalServerError, "failed", false, interaction.ErrDependencyFailure},
		{"oversized body", http.StatusOK, `{"id":"u1","active":true,"extra":"` + strings.Repeat("x", 80) + `"}`, false, interaction.ErrDependencyFailure},
		{"wrong identity", http.StatusOK, `{"id":"u2","active":true}`, false, interaction.ErrDependencyFailure},
		{"invalid JSON", http.StatusOK, `{"id":"u1","active":true} trailing`, false, interaction.ErrDependencyFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/profiles/u1" {
					t.Errorf("unexpected path %q", r.URL.Path)
				}
				w.WriteHeader(tt.status)
				_, _ = fmt.Fprint(w, tt.body)
			}))
			defer server.Close()
			client, err := NewClient(server.URL, server.Client(), 64)
			if err != nil {
				t.Fatal(err)
			}
			participant, err := client.GetParticipant(context.Background(), "u1")
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("got error %v, want %v", err, tt.wantErr)
				}
				return
			}
			if err != nil || participant.ID != "u1" || participant.Active != tt.wantActive {
				t.Fatalf("got participant %#v and error %v", participant, err)
			}
		})
	}
}

func TestClientPropagatesContextDeadline(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	client, err := NewClient(server.URL, server.Client(), 64)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.GetParticipant(ctx, "u1")
	<-started
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v, want deadline exceeded", err)
	}
}

func TestClientRequiresSingleCompleteJSONAndRequiredFields(t *testing.T) {
	for _, body := range []string{
		`{"id":"u1"}`,
		`{"id":"u1","active":true} {}`,
		`{"id":"u1","active":true,"unknown":1}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = fmt.Fprint(w, body)
		}))
		client, err := NewClient(server.URL, server.Client(), 128)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.GetParticipant(context.Background(), "u1")
		server.Close()
		if !errors.Is(err, interaction.ErrDependencyFailure) {
			t.Errorf("body %q returned %v, want dependency failure", body, err)
		}
	}
}
