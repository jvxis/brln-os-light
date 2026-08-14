package privileged

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log"
	osuser "os/user"
	"strconv"
	"strings"
	"time"

	"lightningos-light/internal/config"
	"lightningos-light/internal/lndclient"
)

const (
	defaultLNDTLSCertificatePath       = "/data/lnd/tls.cert"
	defaultLNDGRPCHost                 = "127.0.0.1:10009"
	defaultLNDManagerCredentialRoot    = "/var/lib/lightningos-credentials"
	defaultLNDManagerCredentialDir     = "/var/lib/lightningos-credentials/lnd"
	defaultLNDManagerCredentialState   = "/var/lib/lightningos-credentials/lnd/manager-state.json"
	defaultLNDManagerCredentialUser    = "lightningos"
	defaultLNDManagerCredentialLNDUser = "lnd"
	defaultLNDServiceUnit              = "lnd.service"
	maxLNDManagerCredentialBytes       = 64 * 1024
)

var errInvalidLNDManagerCredential = errors.New("LND returned an invalid manager credential")

var lndManagerPermissions = []lndclient.MacaroonPermission{
	{Entity: "address", Action: "read"},
	{Entity: "address", Action: "write"},
	{Entity: "info", Action: "read"},
	{Entity: "invoices", Action: "read"},
	{Entity: "invoices", Action: "write"},
	{Entity: "macaroon", Action: "generate"},
	{Entity: "macaroon", Action: "read"},
	{Entity: "message", Action: "read"},
	{Entity: "message", Action: "write"},
	{Entity: "offchain", Action: "read"},
	{Entity: "offchain", Action: "write"},
	{Entity: "onchain", Action: "read"},
	{Entity: "onchain", Action: "write"},
	{Entity: "peers", Action: "read"},
	{Entity: "peers", Action: "write"},
	{Entity: "signer", Action: "generate"},
	{Entity: "signer", Action: "read"},
}

type lndManagerCredentialConfig interface {
	LNDMacaroonPath(ctx context.Context) (string, error)
	SetLNDMacaroonPath(ctx context.Context, macaroonPath string, dryRun bool) (bool, error)
}

type lndManagerCredentialRPC interface {
	Bake(ctx context.Context) (credential []byte, rootKeyID uint64, err error)
	Verify(ctx context.Context, macaroonPath string) error
	DeleteRootKey(ctx context.Context, rootKeyID uint64) error
}

type NativeLNDManagerCredentialManager struct {
	credentialRoot string
	credentialDir  string
	credentialPath string
	statePath      string
	adminPath      string
	tlsPath        string
	grpcHost       string
	managerUser    string
	lndUser        string
	lndServiceUnit string
	requireFixed   bool
	runner         CommandRunner
	lookupIdentity func(string) (int, int, error)
	lookupGroupGID func(string) (int, error)
	config         lndManagerCredentialConfig
	rpc            lndManagerCredentialRPC
}

type lndManagerMigrationRecord struct {
	Version      int    `json:"version"`
	Phase        string `json:"phase"`
	RootKeyID    uint64 `json:"root_key_id"`
	PreviousPath string `json:"previous_path"`
	AdminUID     uint32 `json:"admin_uid"`
	AdminGID     uint32 `json:"admin_gid"`
	AdminMode    uint32 `json:"admin_mode"`
}

func NewNativeLNDManagerCredentialManager(files *AtomicConfigFiles, runner CommandRunner) *NativeLNDManagerCredentialManager {
	manager := &NativeLNDManagerCredentialManager{
		credentialRoot: defaultLNDManagerCredentialRoot,
		credentialDir:  defaultLNDManagerCredentialDir,
		credentialPath: DefaultLNDManagerMacaroonPath,
		statePath:      defaultLNDManagerCredentialState,
		adminPath:      DefaultLNDAdminMacaroonPath,
		tlsPath:        defaultLNDTLSCertificatePath,
		grpcHost:       defaultLNDGRPCHost,
		managerUser:    defaultLNDManagerCredentialUser,
		lndUser:        defaultLNDManagerCredentialLNDUser,
		lndServiceUnit: defaultLNDServiceUnit,
		requireFixed:   true,
		runner:         runner,
		lookupIdentity: lookupAppStorageIdentity,
		lookupGroupGID: lookupLNDManagerGroupGID,
		config:         files,
	}
	manager.rpc = newNativeLNDManagerCredentialRPC(manager.grpcHost, manager.tlsPath, manager.adminPath)
	return manager
}

func (manager *NativeLNDManagerCredentialManager) Ensure(ctx context.Context, dryRun bool) (LNDManagerCredentialState, error) {
	managerUID, managerGID, lndUID, lndGID, err := manager.validate(ctx)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	return manager.ensure(ctx, managerUID, managerGID, lndUID, lndGID, dryRun)
}

func (manager *NativeLNDManagerCredentialManager) Rollback(ctx context.Context, dryRun bool) (LNDManagerCredentialState, error) {
	managerUID, managerGID, lndUID, lndGID, err := manager.validate(ctx)
	if err != nil {
		return LNDManagerCredentialState{}, err
	}
	return manager.rollback(ctx, managerUID, managerGID, lndUID, lndGID, dryRun)
}

func (manager *NativeLNDManagerCredentialManager) validate(ctx context.Context) (int, int, int, int, error) {
	if manager == nil || manager.lookupIdentity == nil || manager.config == nil || manager.rpc == nil {
		return 0, 0, 0, 0, errors.New("LND manager credential service is unavailable")
	}
	if manager.requireFixed && (manager.credentialRoot != defaultLNDManagerCredentialRoot ||
		manager.credentialDir != defaultLNDManagerCredentialDir || manager.credentialPath != DefaultLNDManagerMacaroonPath ||
		manager.statePath != defaultLNDManagerCredentialState || manager.adminPath != DefaultLNDAdminMacaroonPath ||
		manager.tlsPath != defaultLNDTLSCertificatePath || manager.grpcHost != defaultLNDGRPCHost ||
		manager.managerUser != defaultLNDManagerCredentialUser || manager.lndUser != defaultLNDManagerCredentialLNDUser ||
		manager.lndServiceUnit != defaultLNDServiceUnit || manager.runner == nil || manager.lookupGroupGID == nil) {
		return 0, 0, 0, 0, errors.New("LND manager credential path policy is invalid")
	}
	managerUID, managerGID, err := manager.lookupIdentity(manager.managerUser)
	if err != nil || managerUID < 1 || managerGID < 1 {
		return 0, 0, 0, 0, errors.New("manager service identity is unavailable")
	}
	lndUID, lndGID, err := manager.resolveLNDServiceIdentity(ctx)
	if err != nil || lndUID < 1 || lndGID < 1 {
		return 0, 0, 0, 0, errors.New("LND service identity is unavailable")
	}
	return managerUID, managerGID, lndUID, lndGID, nil
}

func (manager *NativeLNDManagerCredentialManager) resolveLNDServiceIdentity(ctx context.Context) (int, int, error) {
	// Test managers and legacy in-package fixtures deliberately omit the runner.
	// Production always resolves the fixed lnd.service identity instead of
	// assuming a particular distribution or installer username.
	if manager.runner == nil || manager.lndServiceUnit == "" {
		return manager.lookupIdentity(manager.lndUser)
	}
	serviceUserRaw, err := manager.runner.Run(ctx, systemctlPath, "show", "--property=User", "--value", manager.lndServiceUnit)
	serviceUser := strings.TrimSpace(serviceUserRaw)
	if err != nil || serviceUser == "" || serviceUser == "root" || !systemIntegrationIdentityPattern.MatchString(serviceUser) {
		return 0, 0, errors.New("LND service user is unavailable")
	}
	uid, primaryGID, err := manager.lookupIdentity(serviceUser)
	if err != nil || uid < 1 || primaryGID < 1 {
		return 0, 0, errors.New("LND service user is unavailable")
	}
	serviceGroupRaw, err := manager.runner.Run(ctx, systemctlPath, "show", "--property=Group", "--value", manager.lndServiceUnit)
	serviceGroup := strings.TrimSpace(serviceGroupRaw)
	if err != nil {
		return 0, 0, errors.New("LND service group is unavailable")
	}
	if serviceGroup == "" {
		return uid, primaryGID, nil
	}
	if serviceGroup == "root" || !systemIntegrationIdentityPattern.MatchString(serviceGroup) || manager.lookupGroupGID == nil {
		return 0, 0, errors.New("LND service group is unavailable")
	}
	gid, err := manager.lookupGroupGID(serviceGroup)
	if err != nil || gid < 1 {
		return 0, 0, errors.New("LND service group is unavailable")
	}
	return uid, gid, nil
}

func lookupLNDManagerGroupGID(name string) (int, error) {
	group, err := osuser.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(group.Gid)
}

type nativeLNDManagerCredentialRPC struct {
	grpcHost string
	tlsPath  string
	admin    *lndclient.Client
}

func newNativeLNDManagerCredentialRPC(grpcHost, tlsPath, adminPath string) *nativeLNDManagerCredentialRPC {
	return &nativeLNDManagerCredentialRPC{
		grpcHost: grpcHost,
		tlsPath:  tlsPath,
		admin:    newLNDManagerCredentialClient(grpcHost, tlsPath, adminPath),
	}
}

func newLNDManagerCredentialClient(grpcHost, tlsPath, macaroonPath string) *lndclient.Client {
	shared := false
	cfg := &config.Config{LND: config.LNDConfig{
		GRPCHost:          grpcHost,
		TLSCertPath:       tlsPath,
		AdminMacaroonPath: macaroonPath,
		SharedGRPC:        &shared,
	}}
	return lndclient.New(cfg, log.New(io.Discard, "", 0))
}

func (rpc *nativeLNDManagerCredentialRPC) Bake(ctx context.Context) ([]byte, uint64, error) {
	ids, err := rpc.admin.ListMacaroonIDs(ctx)
	if err != nil {
		return nil, 0, err
	}
	rootKeyID, err := lndclient.GenerateMacaroonRootKeyID(ids, time.Now())
	if err != nil {
		return nil, 0, err
	}
	result, err := rpc.admin.BakeCustomMacaroon(ctx, lndclient.BakeCustomMacaroonRequest{
		Permissions: lndManagerPermissions,
		RootKeyID:   rootKeyID,
	})
	if err != nil {
		return nil, rootKeyID, err
	}
	credential, err := hex.DecodeString(strings.TrimSpace(result.MacaroonHex))
	if err != nil || len(credential) == 0 || len(credential) > maxLNDManagerCredentialBytes {
		return nil, rootKeyID, errInvalidLNDManagerCredential
	}
	return credential, rootKeyID, nil
}

func (rpc *nativeLNDManagerCredentialRPC) Verify(ctx context.Context, macaroonPath string) error {
	pubkey, err := newLNDManagerCredentialClient(rpc.grpcHost, rpc.tlsPath, macaroonPath).SelfPubkey(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(pubkey) == "" {
		return errors.New("LND manager credential verification returned no identity")
	}
	return nil
}

func (rpc *nativeLNDManagerCredentialRPC) DeleteRootKey(ctx context.Context, rootKeyID uint64) error {
	ids, err := rpc.admin.ListMacaroonIDs(ctx)
	if err != nil {
		return err
	}
	present := false
	for _, id := range ids {
		if id == rootKeyID {
			present = true
			break
		}
	}
	if !present {
		return nil
	}
	return rpc.admin.DeleteMacaroonID(ctx, rootKeyID)
}
