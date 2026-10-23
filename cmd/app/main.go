package main

import (
	"context"
	"fmt"
	"log"

	"example.com/linkup/internal/discovery"
	"example.com/linkup/internal/interaction"
	"example.com/linkup/internal/memory"
)

func main() {
	fixtures := []discovery.Profile{
		{ID: "u1", Active: true, Interests: []string{"go", "backend"}},
		{ID: "u2", Active: true, Interests: []string{"go", "music"}},
		{ID: "u3", Active: false, Interests: []string{"go"}},
		{ID: "u4", Active: true, Interests: []string{"backend", "art"}},
		{ID: "u5", Active: true, Interests: []string{"python"}},
	}

	repo := memory.NewProfiles(fixtures)
	service := discovery.NewService(repo)

	targetUserID := "u1"
	recommendations, err := discovery.Recommend(service, targetUserID)
	if err != nil {
		log.Fatalf("Error getting recommendations: %v", err)
	}

	fmt.Printf("Recommendations for user %s: %v\n", targetUserID, recommendations)

	interactionRepository := memory.NewInteractions()
	interactionService := interaction.NewService(interactionRepository)
	ctx := context.Background()
	steps := []struct {
		source, target string
		action         interaction.Status
	}{
		{"u1", "u2", interaction.StatusPending},
		{"u1", "u2", interaction.StatusPending},
		{"u2", "u1", interaction.StatusPending},
		{"u2", "u1", interaction.StatusPending},
	}
	for i, step := range steps {
		result, err := interactionService.React(ctx, step.source, step.target, step.action)
		if err != nil {
			log.Fatalf("interaction step %d failed: %v", i+1, err)
		}
		fmt.Printf("Interaction step %d: changed=%t matched=%t state=%s/%s\n",
			i+1, result.Changed, result.Matched, result.Reaction.SourceID, result.Reaction.Status)
	}
}
