package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"lightningos-light/internal/appmanifest"

	"github.com/jackc/pgx/v5/pgxpool"
)

const appVersionsInitRetryCooldown = 10 * time.Second

// thirdPartyAppCatalogVersions is intentionally separate from lifecycle
// manifests. Updating a release here changes the version offered by the Store,
// but never installs, starts, stops, or upgrades an app by itself.
func thirdPartyAppCatalogVersions() map[string]string {
	return map[string]string{
		appmanifest.BitcoinCoreID:      appmanifest.BitcoinCoreRelease,
		appmanifest.BarkWalletID:       catalogImageVersion(appmanifest.BarkWalletWebImage),
		appmanifest.ElectrsID:          appmanifest.ElectrsRelease,
		appmanifest.MempoolID:          appmanifest.MempoolRelease,
		appmanifest.FedimintGuardianID: appmanifest.FedimintRelease,
		appmanifest.FedimintGatewayID:  appmanifest.FedimintRelease,
		appmanifest.LNDgID:             appmanifest.LNDgRelease,
		appmanifest.LNbitsID:           appmanifest.LNbitsRelease,
		appmanifest.BTCPayID:           appmanifest.BTCPayRelease,
		appmanifest.ElementsID:         appmanifest.ElementsVersion,
		appmanifest.RoboSatsID:         catalogImageVersion(appmanifest.RoboSatsImage),
		appmanifest.PublicPoolID:       appmanifest.PublicPoolBackendVersionOutput,
		appmanifest.TapdID:             appmanifest.TapdRelease,
		appmanifest.LoopID:             appmanifest.LoopVersion,
	}
}

func catalogImageVersion(image string) string {
	withoutDigest, _, _ := strings.Cut(strings.TrimSpace(image), "@")
	separator := strings.LastIndex(withoutDigest, ":")
	if separator < strings.LastIndex(withoutDigest, "/") || separator == len(withoutDigest)-1 {
		return ""
	}
	return withoutDigest[separator+1:]
}

func isVersionedThirdPartyApp(appID string) bool {
	_, ok := thirdPartyAppCatalogVersions()[appID]
	return ok
}

func (s *Server) initAppVersions() {
	s.appVersionsMu.Lock()
	defer s.appVersionsMu.Unlock()

	if s.appVersions != nil && s.appVersionsErr == "" {
		return
	}
	if !s.appVersionsInitAt.IsZero() && time.Since(s.appVersionsInitAt) < appVersionsInitRetryCooldown {
		return
	}
	s.appVersionsInitAt = time.Now()

	pool := s.db
	if pool == nil {
		dsn, err := ResolveNotificationsDSN(s.logger)
		if err != nil {
			s.setAppVersionsInitError(fmt.Errorf("resolve postgres configuration: %w", err))
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.setAppVersionsInitError(fmt.Errorf("connect to postgres: %w", err))
			return
		}
		s.db = pool
	}

	service := NewAppVersionsService(pool)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := service.EnsureSchema(ctx); err != nil {
		s.setAppVersionsInitError(fmt.Errorf("initialize schema: %w", err))
		return
	}
	if err := service.SyncCatalog(ctx, thirdPartyAppCatalogVersions()); err != nil {
		s.setAppVersionsInitError(fmt.Errorf("synchronize catalog: %w", err))
		return
	}

	s.appVersions = service
	s.appVersionsErr = ""
}

func (s *Server) setAppVersionsInitError(err error) {
	s.appVersionsErr = fmt.Sprintf("app version metadata unavailable: %v", err)
	if s.logger != nil {
		s.logger.Printf("%s", s.appVersionsErr)
	}
}

func (s *Server) appVersionsService() (appVersionRepository, string) {
	s.initAppVersions()
	return s.appVersions, s.appVersionsErr
}

func decorateAppVersions(apps []appInfo, versions map[string]appVersionRecord) {
	for index := range apps {
		if !isVersionedThirdPartyApp(apps[index].ID) {
			continue
		}
		record, ok := versions[apps[index].ID]
		if !ok || record.CatalogVersion == "" {
			continue
		}
		apps[index].AvailableVersion = record.CatalogVersion
		if !apps[index].Installed {
			continue
		}
		apps[index].InstalledVersion = record.AppliedVersion
		apps[index].UpdateAvailable = record.AppliedVersion != "" && record.AppliedVersion != record.CatalogVersion
	}
}

func (s *Server) addAppVersions(ctx context.Context, apps []appInfo) {
	service, _ := s.appVersionsService()
	if service == nil {
		return
	}
	versions, err := service.List(ctx)
	if err != nil {
		if s.logger != nil {
			s.logger.Printf("app version metadata unavailable: list versions: %v", err)
		}
		return
	}
	decorateAppVersions(apps, versions)
}

// recordAppliedAppVersion is best-effort bookkeeping after an already
// successful lifecycle operation. Metadata persistence must never turn a
// successful install or start into a user-visible failure.
func (s *Server) recordAppliedAppVersion(ctx context.Context, appID string) {
	if !isVersionedThirdPartyApp(appID) {
		return
	}
	service, _ := s.appVersionsService()
	if service == nil {
		return
	}
	writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := service.MarkApplied(writeCtx, appID); err != nil {
		if s.logger != nil {
			s.logger.Printf("app version metadata unavailable: record applied version for %s: %v", appID, err)
		}
		return
	}
	s.invalidateAppListCache()
}
