package profileapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"example.com/linkup/internal/interaction"
)

type Client struct {
	baseURL string
	http    *http.Client
	limit   int
}

func NewClient(baseURL string, client *http.Client, responseLimit int) (*Client, error) {
	parsed, err := url.ParseRequestURI(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return nil, errors.New("invalid profile API base URL")
	}
	if client == nil {
		return nil, errors.New("profile API HTTP client is required")
	}
	if responseLimit <= 0 {
		return nil, errors.New("profile API response limit must be positive")
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), http: client, limit: responseLimit}, nil
}

func (c *Client) GetParticipant(ctx context.Context, id string) (interaction.Participant, error) {
	endpoint := c.baseURL + "/v1/profiles/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return interaction.Participant{}, fmt.Errorf("%w: create request: %v", interaction.ErrDependencyFailure, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return interaction.Participant{}, fmt.Errorf("%w: request failed: %w", interaction.ErrDependencyFailure, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return interaction.Participant{}, interaction.ErrParticipantNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return interaction.Participant{}, fmt.Errorf("%w: unexpected status %d", interaction.ErrDependencyFailure, resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(c.limit)+1))
	if err != nil {
		return interaction.Participant{}, fmt.Errorf("%w: read response: %v", interaction.ErrDependencyFailure, err)
	}
	if len(data) > c.limit {
		return interaction.Participant{}, fmt.Errorf("%w: response exceeds %d bytes", interaction.ErrDependencyFailure, c.limit)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var dto struct {
		ID     string `json:"id"`
		Active *bool  `json:"active"`
	}
	if err := decoder.Decode(&dto); err != nil {
		return interaction.Participant{}, fmt.Errorf("%w: invalid response JSON: %v", interaction.ErrDependencyFailure, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return interaction.Participant{}, fmt.Errorf("%w: response must contain one JSON value", interaction.ErrDependencyFailure)
	}
	if dto.ID != id || dto.Active == nil {
		return interaction.Participant{}, fmt.Errorf("%w: response does not match requested participant", interaction.ErrDependencyFailure)
	}
	return interaction.Participant{ID: dto.ID, Active: *dto.Active}, nil
}
