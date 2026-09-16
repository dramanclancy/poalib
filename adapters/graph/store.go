package graph

import "context"

// Store fetches acknowledgement documents from a SharePoint site by path.
//
// The drive lookup is cached: every request in a run resolves the same site,
// and the default drive does not change between them.
type Store struct {
	Auth   *GraphAuth
	SiteID string

	driveID string
}

// Get downloads the file at path from the site's default drive.
func (s *Store) Get(_ context.Context, path string) ([]byte, error) {
	if s.driveID == "" {
		id, err := s.Auth.GetDefaultDriveID(s.SiteID)
		if err != nil {
			return nil, err
		}
		s.driveID = id
	}
	return s.Auth.DownloadContentByPath(s.driveID, path)
}
