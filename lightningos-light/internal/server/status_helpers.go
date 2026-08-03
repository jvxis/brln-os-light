package server

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"lightningos-light/internal/system"
)

func (s *Server) postgresStatus(ctx context.Context) postgresResponse {
	entries := postgresDSNEntries()
	databases := make([]postgresDatabase, 0, len(entries))
	version := ""

	for _, entry := range entries {
		dbName := databaseNameFromDSN(entry.DSN)
		if dbName == "" {
			continue
		}
		db := postgresDatabase{
			Name:   dbName,
			Source: entry.Source,
		}
		pool, err := pgxpool.New(ctx, entry.DSN)
		if err == nil {
			db.Available = true
			var sizeBytes int64
			_ = pool.QueryRow(ctx, "select pg_database_size($1)", dbName).Scan(&sizeBytes)
			db.SizeMB = sizeBytes / (1024 * 1024)

			var connections int64
			_ = pool.QueryRow(ctx, "select count(*) from pg_stat_activity where datname=$1", dbName).Scan(&connections)
			db.Connections = connections

			if version == "" {
				_ = pool.QueryRow(ctx, "show server_version").Scan(&version)
			}
			pool.Close()
		}
		databases = append(databases, db)
	}

	dbName := ""
	if s != nil && s.cfg != nil {
		dbName = s.cfg.Postgres.DBName
	}
	resp := postgresResponse{
		ServiceActive: system.SystemctlIsActive(ctx, "postgresql"),
		DBName:        dbName,
		Databases:     databases,
		Version:       version,
	}

	if len(databases) > 0 {
		resp.DBName = databases[0].Name
		resp.DBSizeMB = databases[0].SizeMB
		resp.Connections = databases[0].Connections
	}

	return resp
}

func (s *Server) lndStatus(ctx context.Context, force bool) (lndStatusResponse, error) {
	resp := lndStatusResponse{}
	service := activeLNDService(ctx)
	resp.ServiceActive = service != ""
	if s == nil || s.lnd == nil {
		return resp, errors.New("lnd client unavailable")
	}

	getStatus := s.lnd.GetStatus
	if force {
		getStatus = s.lnd.GetStatusFresh
	}
	status, err := getStatus(ctx)

	resp.WalletState = status.WalletState
	resp.SyncedToChain = status.SyncedToChain
	resp.SyncedToGraph = status.SyncedToGraph
	resp.BlockHeight = status.BlockHeight
	resp.Version = status.Version
	resp.Pubkey = status.Pubkey
	resp.URI = status.URI
	resp.URIs = append([]string(nil), status.URIs...)
	if len(resp.URIs) == 0 {
		trimmedURI := strings.TrimSpace(resp.URI)
		if trimmedURI != "" {
			resp.URIs = []string{trimmedURI}
			resp.URI = trimmedURI
		}
	} else if strings.TrimSpace(resp.URI) == "" {
		resp.URI = resp.URIs[0]
	}
	resp.InfoKnown = status.InfoKnown
	resp.InfoStale = status.InfoStale
	resp.InfoAgeSeconds = status.InfoAgeSeconds
	resp.DBBackend = detectLNDDBBackend()
	if resp.DBBackend == "bolt" {
		if sizeGB, err := lndChannelDBSizeGB(); err == nil {
			resp.ChannelDBSizeGB = &sizeGB
		}
	}
	resp.Channels.Active = status.ChannelsActive
	resp.Channels.Inactive = status.ChannelsInactive
	resp.Channels.Pending = status.ChannelsPending
	resp.Peers.Connected = status.PeersConnected
	resp.Balances.OnchainSat = status.OnchainSat
	resp.Balances.LightningSat = status.LightningSat
	resp.GraphSync = s.graphSyncProgress(service, resp.SyncedToGraph)
	if resp.BlockHeight == 0 {
		resp.BlockHeight = s.lndJournalBlockHeight(service)
	}

	return resp, err
}
