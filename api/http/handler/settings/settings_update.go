package settings

import (
	"net/http"
	"strings"
	"time"

	portainer "github.com/portainer/portainer/api"
	"github.com/portainer/portainer/api/dataservices"
	"github.com/portainer/portainer/api/filesystem"
	"github.com/portainer/portainer/api/internal/edge"
	"github.com/portainer/portainer/pkg/featureflags"
	"github.com/portainer/portainer/pkg/libhelm"
	httperror "github.com/portainer/portainer/pkg/libhttp/error"
	"github.com/portainer/portainer/pkg/libhttp/request"
	"github.com/portainer/portainer/pkg/libhttp/response"

	"github.com/asaskevich/govalidator"
	"github.com/pkg/errors"
)

type settingsUpdatePayload struct {
	// URL to a logo that will be displayed on the login page as well as on top of the sidebar. Will use default Portainer logo when value is empty string
	LogoURL *string `example:"https://mycompany.mydomain.tld/logo.png"`
	// A list of label name & value that will be used to hide containers when querying containers
	BlackListedLabels []portainer.Pair
	// Active authentication method for the Portainer instance. Valid values are: 1 for internal, 2 for LDAP, or 3 for oauth
	AuthenticationMethod *int `example:"1"`
	InternalAuthSettings *portainer.InternalAuthSettings
	LDAPSettings         *portainer.LDAPSettings
	OAuthSettings        *portainer.OAuthSettings
	// The interval in which environment(endpoint) snapshots are created
	SnapshotInterval *string `example:"5m"`
	// URL to the templates that will be displayed in the UI when navigating to App Templates
	TemplatesURL *string `example:"https://raw.githubusercontent.com/portainer/templates/master/templates.json"`
	// The default check in interval for edge agent (in seconds)
	EdgeAgentCheckinInterval *int `example:"5"`
	// Show the Kompose build option (discontinued in 2.18)
	ShowKomposeBuildOption *bool `json:"ShowKomposeBuildOption" example:"false"`
	// Whether edge compute features are enabled
	EnableEdgeComputeFeatures *bool `example:"true"`
	// The duration of a user session
	UserSessionTimeout *string `example:"5m"`
	// The expiry of a Kubeconfig
	KubeconfigExpiry *string `example:"24h" default:"0"`
	// Whether telemetry is enabled
	EnableTelemetry *bool `example:"false"`
	// Helm repository URL
	HelmRepositoryURL *string `example:"https://charts.bitnami.com/bitnami"`
	// Kubectl Shell Image
	KubectlShellImage *string `example:"portainer/kubectl-shell:latest"`
	// TrustOnFirstConnect makes Portainer accepting edge agent connection by default
	TrustOnFirstConnect *bool `example:"false"`
	// EnforceEdgeID makes Portainer store the Edge ID instead of accepting anyone
	EnforceEdgeID *bool `example:"false"`
	// EdgePortainerURL is the URL that is exposed to edge agents
	EdgePortainerURL *string `json:"EdgePortainerURL"`
}

func (payload *settingsUpdatePayload) Validate(r *http.Request) error {
	if payload.AuthenticationMethod != nil && *payload.AuthenticationMethod != 1 && *payload.AuthenticationMethod != 2 && *payload.AuthenticationMethod != 3 {
		return errors.New("Invalid authentication method value. Value must be one of: 1 (internal), 2 (LDAP/AD) or 3 (OAuth)")
	}

	if payload.LogoURL != nil && *payload.LogoURL != "" && !govalidator.IsURL(*payload.LogoURL) {
		return errors.New("Invalid logo URL. Must correspond to a valid URL format")
	}

	if payload.TemplatesURL != nil && *payload.TemplatesURL != "" && !govalidator.IsURL(*payload.TemplatesURL) {
		return errors.New("Invalid external templates URL. Must correspond to a valid URL format")
	}

	if payload.HelmRepositoryURL != nil && *payload.HelmRepositoryURL != "" && !govalidator.IsURL(*payload.HelmRepositoryURL) {
		return errors.New("Invalid Helm repository URL. Must correspond to a valid URL format")
	}

	if payload.UserSessionTimeout != nil {
		_, err := time.ParseDuration(*payload.UserSessionTimeout)
		if err != nil {
			return errors.New("Invalid user session timeout")
		}
	}

	if payload.KubeconfigExpiry != nil {
		_, err := time.ParseDuration(*payload.KubeconfigExpiry)
		if err != nil {
			return errors.New("Invalid Kubeconfig Expiry")
		}
	}

	if payload.EdgePortainerURL != nil && *payload.EdgePortainerURL != "" {
		_, err := edge.ParseHostForEdge(*payload.EdgePortainerURL)
		if err != nil {
			return err
		}
	}

	return nil
}

// @id SettingsUpdate
// @summary Update Portainer settings
// @description Update Portainer settings.
// @description **Access policy**: administrator
// @tags settings
// @security ApiKeyAuth
// @security jwt
// @accept json
// @produce json
// @param body body settingsUpdatePayload true "New settings"
// @success 200 {object} portainer.Settings "Success"
// @failure 400 "Invalid request"
// @failure 500 "Server error"
// @router /settings [put]
func (handler *Handler) settingsUpdate(w http.ResponseWriter, r *http.Request) *httperror.HandlerError {
	var payload settingsUpdatePayload
	err := request.DecodeAndValidateJSONPayload(r, &payload)
	if err != nil {
		return httperror.BadRequest("Invalid request payload", err)
	}

	var settings *portainer.Settings
	if featureflags.IsEnabled(portainer.FeatureNoTx) {
		settings, err = handler.updateSettings(handler.DataStore, payload)
	} else {
		err = handler.DataStore.UpdateTx(func(tx dataservices.DataStoreTx) error {
			settings, err = handler.updateSettings(tx, payload)
			return err
		})
	}

	if err != nil {
		var httpErr *httperror.HandlerError
		if errors.As(err, &httpErr) {
			return httpErr
		}

		return httperror.InternalServerError("Unexpected error", err)
	}

	hideFields(settings)
	return response.JSON(w, settings)
}

func (handler *Handler) updateSettings(tx dataservices.DataStoreTx, payload settingsUpdatePayload) (*portainer.Settings, error) {
	settings, err := tx.Settings().Settings()
	if err != nil {
		return nil, httperror.InternalServerError("Unable to retrieve the settings from the database", err)
	}

	if handler.demoService.IsDemo() {
		payload.EnableTelemetry = nil
		payload.LogoURL = nil
	}

	if payload.AuthenticationMethod != nil {
		settings.AuthenticationMethod = portainer.AuthenticationMethod(*payload.AuthenticationMethod)
	}

	if payload.LogoURL != nil {
		settings.LogoURL = *payload.LogoURL
	}

	if payload.TemplatesURL != nil {
		settings.TemplatesURL = *payload.TemplatesURL
	}

	if payload.ShowKomposeBuildOption != nil {
		settings.ShowKomposeBuildOption = *payload.ShowKomposeBuildOption
	}

	if payload.HelmRepositoryURL != nil {
		if *payload.HelmRepositoryURL != "" {

			newHelmRepo := strings.TrimSuffix(strings.ToLower(*payload.HelmRepositoryURL), "/")

			if newHelmRepo != settings.HelmRepositoryURL && newHelmRepo != portainer.DefaultHelmRepositoryURL {
				err := libhelm.ValidateHelmRepositoryURL(*payload.HelmRepositoryURL, nil)
				if err != nil {
					return nil, httperror.BadRequest("Invalid Helm repository URL. Must correspond to a valid URL format", err)
				}
			}

			settings.HelmRepositoryURL = newHelmRepo
		} else {
			settings.HelmRepositoryURL = ""
		}
	}

	if payload.BlackListedLabels != nil {
		settings.BlackListedLabels = payload.BlackListedLabels
	}

	if payload.InternalAuthSettings != nil {
		settings.InternalAuthSettings.RequiredPasswordLength = payload.InternalAuthSettings.RequiredPasswordLength
	}

	if payload.LDAPSettings != nil {
		ldapReaderDN := settings.LDAPSettings.ReaderDN
		ldapPassword := settings.LDAPSettings.Password

		if payload.LDAPSettings.ReaderDN != "" {
			ldapReaderDN = payload.LDAPSettings.ReaderDN
		}

		if payload.LDAPSettings.Password != "" {
			ldapPassword = payload.LDAPSettings.Password
		}

		settings.LDAPSettings = *payload.LDAPSettings
		settings.LDAPSettings.ReaderDN = ldapReaderDN
		settings.LDAPSettings.Password = ldapPassword
	}

	if payload.OAuthSettings != nil {
		clientSecret := payload.OAuthSettings.ClientSecret
		if clientSecret == "" {
			clientSecret = settings.OAuthSettings.ClientSecret
		}

		kubeSecret := payload.OAuthSettings.KubeSecretKey
		if kubeSecret == nil {
			kubeSecret = settings.OAuthSettings.KubeSecretKey
		}
		settings.OAuthSettings = *payload.OAuthSettings
		settings.OAuthSettings.ClientSecret = clientSecret
		settings.OAuthSettings.KubeSecretKey = kubeSecret
	}

	if payload.EnableEdgeComputeFeatures != nil {
		settings.EnableEdgeComputeFeatures = *payload.EnableEdgeComputeFeatures
	}

	if payload.TrustOnFirstConnect != nil {
		settings.TrustOnFirstConnect = *payload.TrustOnFirstConnect
	}

	if payload.EnforceEdgeID != nil {
		settings.EnforceEdgeID = *payload.EnforceEdgeID
	}

	if payload.EdgePortainerURL != nil {
		settings.EdgePortainerURL = *payload.EdgePortainerURL
	}

	if payload.SnapshotInterval != nil && *payload.SnapshotInterval != settings.SnapshotInterval {
		err := handler.updateSnapshotInterval(settings, *payload.SnapshotInterval)
		if err != nil {
			return nil, httperror.InternalServerError("Unable to update snapshot interval", err)
		}
	}

	if payload.EdgeAgentCheckinInterval != nil {
		settings.EdgeAgentCheckinInterval = *payload.EdgeAgentCheckinInterval
	}

	if payload.KubeconfigExpiry != nil {
		settings.KubeconfigExpiry = *payload.KubeconfigExpiry
	}

	if payload.UserSessionTimeout != nil {
		settings.UserSessionTimeout = *payload.UserSessionTimeout

		userSessionDuration, _ := time.ParseDuration(*payload.UserSessionTimeout)

		handler.JWTService.SetUserSessionDuration(userSessionDuration)
	}

	if payload.EnableTelemetry != nil {
		settings.EnableTelemetry = *payload.EnableTelemetry
	}

	err = handler.updateTLS(settings)
	if err != nil {
		return nil, err
	}

	if payload.KubectlShellImage != nil {
		settings.KubectlShellImage = *payload.KubectlShellImage
	}

	err = tx.Settings().UpdateSettings(settings)
	if err != nil {
		return nil, httperror.InternalServerError("Unable to persist settings changes inside the database", err)
	}

	return settings, nil
}

func (handler *Handler) updateSnapshotInterval(settings *portainer.Settings, snapshotInterval string) error {
	settings.SnapshotInterval = snapshotInterval

	return handler.SnapshotService.SetSnapshotInterval(snapshotInterval)
}

func (handler *Handler) updateTLS(settings *portainer.Settings) error {
	if (settings.LDAPSettings.TLSConfig.TLS || settings.LDAPSettings.StartTLS) && !settings.LDAPSettings.TLSConfig.TLSSkipVerify {
		caCertPath, _ := handler.FileService.GetPathForTLSFile(filesystem.LDAPStorePath, portainer.TLSFileCA)
		settings.LDAPSettings.TLSConfig.TLSCACertPath = caCertPath

		return nil
	}

	settings.LDAPSettings.TLSConfig.TLSCACertPath = ""
	err := handler.FileService.DeleteTLSFiles(filesystem.LDAPStorePath)
	if err != nil {
		return httperror.InternalServerError("Unable to remove TLS files from disk", err)
	}

	return nil
}
