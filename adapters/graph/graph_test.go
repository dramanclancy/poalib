//go:build integration

package graph

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joho/godotenv"
)

// testClient loads credentials from poalib/.env (or POALIB_ENV_PATH, if
// set, as an override) and skips the test when they're absent. These are
// live Microsoft Graph tests requiring real Azure credentials — hence the
// integration build tag, run with `go test -tags integration ./...`.
func testClient(t *testing.T) *GraphAuth {
	t.Helper()

	envPath := os.Getenv("POALIB_ENV_PATH")
	if envPath == "" {
		envPath = filepath.Join("..", ".env") // poalib/.env, relative to this package
	}
	godotenv.Load(envPath)

	cfg := Appconfig{
		TenantID:     os.Getenv("AZURE_TENANT_ID"),
		ClientID:     os.Getenv("AZURE_CLIENT_ID"),
		ClientSecret: os.Getenv("AZURE_CLIENT_SECRET"),
	}
	if cfg.TenantID == "" {
		t.Skip("no creds")
	}

	g, err := NewGraphAuth(cfg)
	if err != nil {
		t.Fatalf("graph auth: %v", err)
	}
	return g
}

func TestGetDefaultDriveID(t *testing.T) {
	g := testClient(t)

	siteID := os.Getenv("SHAREPOINT_SITE_ID")
	if siteID == "" {
		t.Skip("skipping: SHAREPOINT_SITE_ID not set")
	}

	driveID, err := g.GetDefaultDriveID(siteID)
	if err != nil {
		t.Fatalf("GetDefaultDriveID returned error: %v", err)
	}
	t.Logf("DriveID: %s", driveID)
}

func TestGetItemByPath(t *testing.T) {
	g := testClient(t)

	siteID := os.Getenv("SHAREPOINT_SITE_ID")
	if siteID == "" {
		t.Skip("skipping: SHAREPOINT_SITE_ID not set")
	}

	driveID, err := g.GetDefaultDriveID(siteID)
	if err != nil {
		t.Fatalf("GetDefaultDriveID returned error: %v", err)
	}
	path := "POA"

	output, err := g.GetItemByPath(driveID, path)
	if err != nil {
		t.Fatalf("GetItemByPath: %v", err)
	}

	t.Logf("Name: %s", *output.GetName())
	t.Logf("ID:   %s", *output.GetId())
	t.Logf("Size: %d bytes", *output.GetSize())
	if output.GetFolder() != nil {
		t.Logf("This is a folder with %d children", *output.GetFolder().GetChildCount())
	}
}
