package server

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const channelOpenCandidatesInitRetryCooldown = 10 * time.Second

func (s *Server) initChannelOpenCandidates() {
	s.channelOpenCandidatesMu.Lock()
	defer s.channelOpenCandidatesMu.Unlock()

	if s.channelOpenCandidates != nil && s.channelOpenCandidatesErr == "" {
		return
	}
	if !s.channelOpenCandidatesInitAt.IsZero() && time.Since(s.channelOpenCandidatesInitAt) < channelOpenCandidatesInitRetryCooldown {
		return
	}
	s.channelOpenCandidatesInitAt = time.Now()

	dsn, err := ResolveNotificationsDSN(s.logger)
	if err != nil {
		s.channelOpenCandidatesErr = fmt.Sprintf("channel open candidates unavailable: %v", err)
		s.logger.Printf("%s", s.channelOpenCandidatesErr)
		return
	}

	pool := s.db
	if pool == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pool, err = pgxpool.New(ctx, dsn)
		if err != nil {
			s.channelOpenCandidatesErr = fmt.Sprintf("channel open candidates unavailable: failed to connect to postgres: %v", err)
			s.logger.Printf("%s", s.channelOpenCandidatesErr)
			return
		}
		s.db = pool
	}

	svc := NewChannelOpenCandidatesService(pool, s.logger, s.lnd)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := svc.EnsureSchema(ctx); err != nil {
		s.channelOpenCandidatesErr = fmt.Sprintf("channel open candidates unavailable: failed to init schema: %v", err)
		s.logger.Printf("%s", s.channelOpenCandidatesErr)
		return
	}

	s.channelOpenCandidates = svc
	s.channelOpenCandidatesErr = ""
}

func (s *Server) channelOpenCandidatesService() (*ChannelOpenCandidatesService, string) {
	s.initChannelOpenCandidates()
	return s.channelOpenCandidates, s.channelOpenCandidatesErr
}
