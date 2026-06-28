package server

import "context"

const (
	appsRoot     = "/var/lib/lightningos/apps"
	appsDataRoot = "/var/lib/lightningos/apps-data"
)

type appDefinition struct {
	ID          string
	Name        string
	Description string
	Port        int
	ExternalURL string
}

type appInfo struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Description        string `json:"description"`
	Installed          bool   `json:"installed"`
	Status             string `json:"status"`
	Port               int    `json:"port"`
	ExternalURL        string `json:"external_url,omitempty"`
	AdminPasswordPath  string `json:"admin_password_path,omitempty"`
	Available          bool   `json:"available"`
	UnavailableReason  string `json:"unavailable_reason,omitempty"`
	UnavailableMessage string `json:"unavailable_message,omitempty"`
	UFWActive          bool   `json:"ufw_active,omitempty"`
	UFWCommand         string `json:"ufw_command,omitempty"`
}

type appHandler interface {
	Definition() appDefinition
	Info(ctx context.Context) (appInfo, error)
	Install(ctx context.Context) error
	Uninstall(ctx context.Context) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

func newAppInfo(def appDefinition) appInfo {
	return appInfo{
		ID:          def.ID,
		Name:        def.Name,
		Description: def.Description,
		Installed:   false,
		Status:      "not_installed",
		Port:        def.Port,
		ExternalURL: def.ExternalURL,
		Available:   true,
	}
}
