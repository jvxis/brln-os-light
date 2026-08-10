package appmanifest

import "errors"

// ComposeManifest is closed catalog metadata shared by the unprivileged
// manager and privileged broker. It contains no caller-controlled paths,
// images, services, or command arguments.
type ComposeManifest struct {
	ID                 string
	Project            string
	ComposeFile        string
	EnvFile            string
	PrimaryService     string
	StopTimeoutSeconds int
}

func ComposeManifestForApp(appID string) (ComposeManifest, error) {
	switch appID {
	case CPUMinerID:
		return ComposeManifest{
			ID:                 CPUMinerID,
			Project:            CPUMinerProject,
			ComposeFile:        CPUMinerComposeFile,
			EnvFile:            CPUMinerEnvFile,
			PrimaryService:     CPUMinerID,
			StopTimeoutSeconds: 2,
		}, nil
	case RoboSatsID:
		return ComposeManifest{
			ID:                 RoboSatsID,
			Project:            RoboSatsProject,
			ComposeFile:        RoboSatsComposeFile,
			PrimaryService:     RoboSatsPrimaryService,
			StopTimeoutSeconds: 2,
		}, nil
	default:
		return ComposeManifest{}, errors.New("compose app manifest is not allowed")
	}
}

func CatalogImageForVariant(appID string, variant AppImageVariant) (string, error) {
	switch appID {
	case CPUMinerID:
		return CPUMinerImageForVariant(variant)
	case RoboSatsID:
		return RoboSatsImageForVariant(variant)
	default:
		return "", errors.New("app image manifest is not allowed")
	}
}
