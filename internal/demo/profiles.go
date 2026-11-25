package demo

import "example.com/linkup/internal/discovery"

func Profiles() []discovery.Profile {
	return []discovery.Profile{
		{ID: "u1", Active: true, Interests: []string{"go", "backend"}},
		{ID: "u2", Active: true, Interests: []string{"go", "music"}},
		{ID: "u3", Active: false, Interests: []string{"go"}},
		{ID: "u4", Active: true, Interests: []string{"backend", "art"}},
		{ID: "u5", Active: true, Interests: []string{"go", "python"}},
		{ID: "u6", Active: true, Interests: []string{"go", "design"}},
	}
}
