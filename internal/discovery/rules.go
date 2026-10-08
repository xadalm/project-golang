package discovery

func Discover(subject Profile, candidates []Profile) []string {
	result := make([]string, 0)

	subjectInterests := make(map[string]bool, len(subject.Interests))
	for _, interest := range subject.Interests {
		subjectInterests[interest] = true
	}

	for _, candidate := range candidates {
		if candidate.ID == subject.ID || !candidate.Active {
			continue
		}

		hasMatch := false
		for _, interest := range candidate.Interests {
			if subjectInterests[interest] {
				hasMatch = true
				break
			}
		}

		if hasMatch {
			result = append(result, candidate.ID)
		}
	}

	return result
}
