package graph

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/microsoftgraph/msgraph-sdk-go/drives"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/sites"
)

// pathURL is the single place raw Graph URLs are constructed.
// Used only for path addressing (root:/path:), which the Go fluent API cannot express.
// suffix is appended after the closing colon, e.g. "/content" or "/children".
func pathURL(driveID string, itemPath string, suffix string) string {
	segments := strings.Split(itemPath, "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return fmt.Sprintf(
		"https://graph.microsoft.com/v1.0/drives/%s/root:/%s:%s",
		driveID, strings.Join(segments, "/"), suffix,
	)
}

// GetDefaultDriveID returns the ID of the site's default document library.
// Fluent chain — ID-based addressing, with $select to fetch only what we use.
func (g *GraphAuth) GetDefaultDriveID(siteID string) (string, error) {
	query := sites.ItemDriveRequestBuilderGetQueryParameters{
		Select: []string{"id"},
	}

	drive, err := g.appClient.
		Sites().
		BySiteId(siteID).
		Drive().
		Get(context.Background(),
			&sites.ItemDriveRequestBuilderGetRequestConfiguration{
				QueryParameters: &query,
			})
	if err != nil {
		return "", err
	}
	if drive == nil || drive.GetId() == nil {
		return "", fmt.Errorf("msgraph: site %s has no default drive id", siteID)
	}
	return *drive.GetId(), nil
}

// GetItemByPath returns a drive item's metadata, addressed by path relative to the drive root.
// Raw URL — path addressing has no fluent equivalent in the Go SDK.
func (g *GraphAuth) GetItemByPath(driveID string, itemPath string) (models.DriveItemable, error) {
	query := drives.ItemItemsDriveItemItemRequestBuilderGetQueryParameters{
		Select: []string{"id", "name", "size", "file", "folder", "webUrl"},
	}

	builder := drives.NewItemItemsDriveItemItemRequestBuilder(
		pathURL(driveID, itemPath, ""),
		g.appClient.GetAdapter(),
	)

	return builder.Get(context.Background(),
		&drives.ItemItemsDriveItemItemRequestBuilderGetRequestConfiguration{
			QueryParameters: &query,
		})
}

// DownloadContentByPath returns the raw bytes of a file, addressed by path.
// Raw URL — path addressing. No query parameters: /content returns the byte stream.
func (g *GraphAuth) DownloadContentByPath(driveID string, itemPath string) ([]byte, error) {
	builder := drives.NewItemItemsItemContentRequestBuilder(
		pathURL(driveID, itemPath, "/content"),
		g.appClient.GetAdapter(),
	)

	return builder.Get(context.Background(), nil)
}
