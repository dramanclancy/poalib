package graph

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/microsoftgraph/msgraph-sdk-go/drives"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/models/odataerrors"
	"github.com/microsoftgraph/msgraph-sdk-go/sites"
)

// cleanPath gives a caller's item path the form Graph addresses by: forward
// slashes, none leading or trailing.
//
// A request arrived on 2026-11-12 with a Windows-style path
// ("POA\noreply@supplier\Scanned File\x.pdf"). Split on "/" alone, that is
// one segment, sent as a single file name with %5C in it, which Graph can
// only answer with itemNotFound. SharePoint forbids "\"
// in names, so a backslash can only ever have been meant as a separator.
func cleanPath(itemPath string) string {
	return strings.Trim(strings.ReplaceAll(itemPath, `\`, "/"), "/")
}

// pathErr adds what Graph's error leaves out: which path was asked for, and
// the status and code. "The resource could not be found." alone does not say
// whether the file, a folder above it, or the site was missing — and the path
// printed here is the one actually requested, after cleanPath.
func pathErr(itemPath string, err error) error {
	var od *odataerrors.ODataError
	if !errors.As(err, &od) {
		return fmt.Errorf("sharepoint: %q: %w", cleanPath(itemPath), err)
	}
	code := ""
	if main := od.GetErrorEscaped(); main != nil && main.GetCode() != nil {
		code = " " + *main.GetCode()
	}
	return fmt.Errorf("sharepoint: %q in the site's default library: HTTP %d%s: %w",
		cleanPath(itemPath), od.ResponseStatusCode, code, err)
}

// pathURL is the single place raw Graph URLs are constructed.
// Used only for path addressing (root:/path:), which the Go fluent API cannot express.
// suffix is appended after the closing colon, e.g. "/content" or "/children".
func pathURL(driveID string, itemPath string, suffix string) string {
	segments := strings.Split(cleanPath(itemPath), "/")
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
func (g *GraphAuth) GetDefaultDriveID(ctx context.Context, siteID string) (string, error) {
	query := sites.ItemDriveRequestBuilderGetQueryParameters{
		Select: []string{"id"},
	}

	drive, err := g.appClient.
		Sites().
		BySiteId(siteID).
		Drive().
		Get(ctx,
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
func (g *GraphAuth) GetItemByPath(ctx context.Context, driveID string, itemPath string) (models.DriveItemable, error) {
	query := drives.ItemItemsDriveItemItemRequestBuilderGetQueryParameters{
		Select: []string{"id", "name", "size", "file", "folder", "webUrl"},
	}

	builder := drives.NewItemItemsDriveItemItemRequestBuilder(
		pathURL(driveID, itemPath, ""),
		g.appClient.GetAdapter(),
	)

	return builder.Get(ctx,
		&drives.ItemItemsDriveItemItemRequestBuilderGetRequestConfiguration{
			QueryParameters: &query,
		})
}

// DownloadContentByPath returns the raw bytes of a file, addressed by path.
// Raw URL — path addressing. No query parameters: /content returns the byte stream.
func (g *GraphAuth) DownloadContentByPath(ctx context.Context, driveID string, itemPath string) ([]byte, error) {
	builder := drives.NewItemItemsItemContentRequestBuilder(
		pathURL(driveID, itemPath, "/content"),
		g.appClient.GetAdapter(),
	)

	data, err := builder.Get(ctx, nil)
	if err != nil {
		return nil, pathErr(itemPath, err)
	}
	return data, nil
}
