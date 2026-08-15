package server

import (
	"context"
	"time"
)

const (
	appsRoot     = "/var/lib/lightningos/apps"
	appsDataRoot = "/var/lib/lightningos/apps-data"

	appSecurityNoticeElevatedLNDAccess    = "elevated_lnd_access"
	appSecurityNoticeLimitedLNDAccess     = "limited_lnd_access"
	appSecurityNoticeLNDDataDirectoryRead = "lnd_data_directory_read"
)

type appDefinition struct {
	ID          string
	Name        string
	Description string
	Port        int
	ExternalURL string
	// SecurityNotices describes elevated access granted to the app. These are
	// informational disclosures only; they must never change app lifecycle or
	// installation behavior on their own.
	SecurityNotices []string
}

type appInfo struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	Installed          bool              `json:"installed"`
	Status             string            `json:"status"`
	Port               int               `json:"port"`
	Scheme             string            `json:"scheme,omitempty"`
	ExternalURL        string            `json:"external_url,omitempty"`
	AdminPasswordPath  string            `json:"admin_password_path,omitempty"`
	Available          bool              `json:"available"`
	UnavailableReason  string            `json:"unavailable_reason,omitempty"`
	UnavailableMessage string            `json:"unavailable_message,omitempty"`
	UFWActive          bool              `json:"ufw_active,omitempty"`
	UFWCommand         string            `json:"ufw_command,omitempty"`
	SecurityNotices    []string          `json:"security_notices,omitempty"`
	Operation          *appOperationInfo `json:"operation,omitempty"`
}

type appOperationInfo struct {
	Action    string    `json:"action"`
	StartedAt time.Time `json:"started_at"`
	Stage     string    `json:"stage,omitempty"`
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
		ID:              def.ID,
		Name:            def.Name,
		Description:     def.Description,
		Installed:       false,
		Status:          "not_installed",
		Port:            def.Port,
		Scheme:          "http",
		ExternalURL:     def.ExternalURL,
		Available:       true,
		SecurityNotices: append([]string(nil), def.SecurityNotices...),
	}
}
