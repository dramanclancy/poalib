// Package graph wraps Microsoft Graph auth and SharePoint file I/O.
package graph

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	auth "github.com/microsoft/kiota-authentication-azure-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
)

type Appconfig struct {
	// The tenant ID of the Azure AD tenant
	TenantID string
	// The client ID of the Azure AD application
	ClientID string
	// The client secret of the Azure AD application
	ClientSecret string
}

// GraphAuth is an authenticated Microsoft Graph client for one app
// registration.
type GraphAuth struct {
	appClient *msgraphsdk.GraphServiceClient
}

func NewGraphAuth(cfg Appconfig) (*GraphAuth, error) {
	credential, err := azidentity.NewClientSecretCredential(cfg.TenantID, cfg.ClientID, cfg.ClientSecret, nil)
	if err != nil {
		return nil, err
	}

	authProvider, err := auth.NewAzureIdentityAuthenticationProviderWithScopes(
		credential, []string{"https://graph.microsoft.com/.default"})
	if err != nil {
		return nil, err
	}

	adapter, err := msgraphsdk.NewGraphRequestAdapter(authProvider)
	if err != nil {
		return nil, err
	}

	return &GraphAuth{appClient: msgraphsdk.NewGraphServiceClient(adapter)}, nil
}
