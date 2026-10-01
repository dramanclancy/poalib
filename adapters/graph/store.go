package graph

import (
	"context"
	"sync"
)

// Store fetches acknowledgement documents from a SharePoint site by path.
//
// The drive lookup is cached: every request in a run resolves the same site,
// and the default drive does not change between them. One Store serves every
// concurrent request, so the cache is guarded; a failed lookup is not cached
// and the next request tries again.
type Store struct {
	Auth   *GraphAuth
	SiteID string

	mu      sync.Mutex
	driveID string
}

// Get downloads the file at path from the site's default drive.
func (s *Store) Get(ctx context.Context, path string) ([]byte, error) {
	driveID, err := s.drive(ctx)
	if err != nil {
		return nil, err
	}
	return s.Auth.DownloadContentByPath(ctx, driveID, path)
}

func (s *Store) drive(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.driveID == "" {
		id, err := s.Auth.GetDefaultDriveID(ctx, s.SiteID)
		if err != nil {
			return "", err
		}
		s.driveID = id
	}
	return s.driveID, nil
}
