package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const channelRankingInitRetryCooldown = 10 * time.Second

func (s *Server) initChannelRanking() {
	s.channelRankingMu.Lock()
	defer s.channelRankingMu.Unlock()

	if s.channelRanking != nil && s.channelRankingErr == "" {
		return
	}
	if !s.channelRankingInitAt.IsZero() && time.Since(s.channelRankingInitAt) < channelRankingInitRetryCooldown {
		return
	}
	s.channelRankingInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.channelRankingErr = fmt.Sprintf("channel ranking unavailable: %v", err)
		s.logger.Printf("%s", s.channelRankingErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.channelRankingErr = fmt.Sprintf("channel ranking unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.channelRankingErr)
			return
		}
		s.db = pool
	}

	svc := NewChannelRankingService(pool, s.logger, s.lnd)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.channelRankingErr = fmt.Sprintf("channel ranking unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.channelRankingErr)
		return
	}

	s.channelRanking = svc
	s.channelRankingErr = ""
}

func (s *Server) channelRankingService() (*ChannelRankingService, string) {
	s.initChannelRanking()
	return s.channelRanking, s.channelRankingErr
}
