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
	case BitcoinCoreID:
		return ComposeManifest{
			ID:                 BitcoinCoreID,
			Project:            BitcoinCoreProject,
			ComposeFile:        BitcoinCoreComposeFile,
			PrimaryService:     BitcoinCorePrimaryService,
			StopTimeoutSeconds: BitcoinCoreStopTimeout,
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
	case BitcoinCoreID:
		return BitcoinCoreImageForVariant(variant)
	case BTCPayID:
		return BTCPayImageForVariant(variant)
	default:
		return "", errors.New("app image manifest is not allowed")
	}
}

// CatalogImageRequiresRefresh distinguishes release tags that must be checked
// on every requested start from cache-authoritative or locally attested
// artifacts. The catalog remains the sole authority for this policy.
func CatalogImageRequiresRefresh(appID string, variant AppImageVariant) (bool, error) {
	if _, err := CatalogImageForVariant(appID, variant); err != nil {
		return false, err
	}
	return appID == BTCPayID && variant == BTCPayImageServer, nil
}

func CatalogExternalTCPPort(appID string) (int, error) {
	switch appID {
	case RoboSatsID:
		return RoboSatsPort, nil
	default:
		return 0, errors.New("app external access manifest is not allowed")
	}
}
