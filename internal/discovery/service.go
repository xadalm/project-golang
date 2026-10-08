package discovery

import "fmt"

type ProfileReader interface {
	Get(id string) (Profile, error)
	List() ([]Profile, error)
}

type Service struct {
	reader ProfileReader
}

func NewService(reader ProfileReader) *Service {
	return &Service{
		reader: reader,
	}
}

func Recommend(s *Service, userID string) ([]string, error) {
	subject, err := s.reader.Get(userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get subject profile %s: %w", userID, err)
	}

	candidates, err := s.reader.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list candidate profiles: %w", err)
	}

	result := Discover(subject, candidates)

	return result, nil
}
