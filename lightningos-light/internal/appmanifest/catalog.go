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
	RemoveVolumes      bool
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
	case BTCPayID:
		return ComposeManifest{
			ID:                 BTCPayID,
			Project:            BTCPayProject,
			ComposeFile:        BTCPayComposeFile,
			EnvFile:            BTCPayEnvFile,
			PrimaryService:     BTCPayPrimaryService,
			StopTimeoutSeconds: BTCPayStopTimeout,
		}, nil
	case LNDgID:
		return ComposeManifest{
			ID:                 LNDgID,
			Project:            LNDgProject,
			ComposeFile:        LNDgComposeFile,
			EnvFile:            LNDgEnvFile,
			PrimaryService:     LNDgPrimaryService,
			StopTimeoutSeconds: LNDgStopTimeout,
		}, nil
	case LNbitsID:
		return ComposeManifest{
			ID:                 LNbitsID,
			Project:            LNbitsProject,
			ComposeFile:        LNbitsComposeFile,
			EnvFile:            LNbitsEnvFile,
			PrimaryService:     LNbitsPrimaryService,
			StopTimeoutSeconds: LNbitsStopTimeout,
		}, nil
	case ElectrsID:
		return ComposeManifest{
			ID:                 ElectrsID,
			Project:            ElectrsProject,
			ComposeFile:        ElectrsComposeFile,
			EnvFile:            ElectrsEnvFile,
			PrimaryService:     ElectrsPrimaryService,
			StopTimeoutSeconds: ElectrsStopTimeout,
			RemoveVolumes:      true,
		}, nil
	case MempoolID:
		return ComposeManifest{
			ID: MempoolID, Project: MempoolProject, ComposeFile: MempoolComposeFile,
			EnvFile: MempoolEnvFile, PrimaryService: MempoolPrimaryService,
			StopTimeoutSeconds: MempoolStopTimeout, RemoveVolumes: true,
		}, nil
	case FedimintGuardianID:
		return ComposeManifest{
			ID: FedimintGuardianID, Project: FedimintGuardianProject, ComposeFile: FedimintGuardianComposeFile,
			PrimaryService: FedimintGuardianPrimaryService, StopTimeoutSeconds: FedimintStopTimeout,
		}, nil
	case FedimintGatewayID:
		return ComposeManifest{
			ID: FedimintGatewayID, Project: FedimintGatewayProject, ComposeFile: FedimintGatewayComposeFile,
			PrimaryService: FedimintGatewayPrimaryService, StopTimeoutSeconds: FedimintStopTimeout,
		}, nil
	case TapdID:
		return ComposeManifest{
			ID:                 TapdID,
			Project:            TapdProject,
			ComposeFile:        TapdComposeFile,
			PrimaryService:     TapdPrimaryService,
			StopTimeoutSeconds: TapdStopTimeout,
		}, nil
	case PublicPoolID:
		return ComposeManifest{
			ID: PublicPoolID, Project: PublicPoolProject, ComposeFile: PublicPoolComposeFile,
			EnvFile: PublicPoolEnvFile, PrimaryService: PublicPoolPrimaryService,
			StopTimeoutSeconds: PublicPoolStopTimeout,
		}, nil
	case BarkWalletID:
		return ComposeManifest{
			ID: BarkWalletID, Project: BarkWalletProject, ComposeFile: BarkWalletComposeFile,
			PrimaryService: BarkWalletPrimaryService, StopTimeoutSeconds: BarkWalletStopTimeout,
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
	case LNDgID:
		return LNDgImageForVariant(variant)
	case LNbitsID:
		return LNbitsImageForVariant(variant)
	case ElectrsID:
		return ElectrsImageForVariant(variant)
	case MempoolID:
		return MempoolImageForVariant(variant)
	case FedimintGuardianID, FedimintGatewayID:
		return FedimintImageForApp(appID, variant)
	case TapdID:
		return TapdImageForVariant(variant)
	case PublicPoolID:
		return PublicPoolImageForVariant(variant)
	case BarkWalletID:
		return BarkWalletImageForVariant(variant)
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
	case LNDgID:
		return LNDgPort, nil
	case LNbitsID:
		return LNbitsPort, nil
	case PeerSwapID:
		return PeerSwapWebPort, nil
	case BarkWalletID:
		return BarkWalletPort, nil
	case MempoolID:
		return MempoolPort, nil
	case FedimintGuardianID:
		return FedimintGuardianUIPort, nil
	case FedimintGatewayID:
		return FedimintGatewayUIPort, nil
	default:
		return 0, errors.New("app external access manifest is not allowed")
	}
}
