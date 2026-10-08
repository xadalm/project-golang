package main

import (
	"fmt"
	"log"

	"example.com/linkup/internal/discovery"
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
}
