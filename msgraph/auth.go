// Package msgraph wraps Microsoft Graph auth and SharePoint file I/O.
package msgraph

import (
	"context"
	

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	
	auth "github.com/microsoft/kiota-authentication-azure-go"
	msgraphsdk "github.com/microsoftgraph/msgraph-sdk-go"
	
)
const (
	ScopeGraph = "https://graph.microsoft.com/.default"
	ScopeBC    = "https://api.businesscentral.dynamics.com/.default"
)

type Appconfig struct {
	// The tenant ID of the Azure AD tenant
	TenantID string
	// The client ID of the Azure AD application
	ClientID string
	// The client secret of the Azure AD application
	ClientSecret string
}

type GraphAuth struct {
	//client secret for the application
	clientSecretCredential *azidentity.ClientSecretCredential
	//Graph service client
	appClient              *msgraphsdk.GraphServiceClient
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

	return &GraphAuth{
		clientSecretCredential: credential,
		appClient:              msgraphsdk.NewGraphServiceClient(adapter),
	}, nil
}

func (g *GraphAuth) GetAppToken(scope string) (string, error) {
	token, err := g.clientSecretCredential.GetToken(context.Background(), policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return "", err
	}

	return token.Token, nil
}

func (g *GraphAuth) GetGraphToken() (string, error) { return g.GetAppToken(ScopeGraph) }
func (g *GraphAuth) GetBCToken() (string, error)    { return g.GetAppToken(ScopeBC) }
// InitGraphAuth initializes the GraphAuth instance with the provided app configuration.

