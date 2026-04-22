package service

import (
	"context"

	"git.aegis-hq.xyz/coldforge/cloistr-blossom/db"
	"git.aegis-hq.xyz/coldforge/cloistr-blossom/src/core"
)

type statService struct {
	queries *db.Queries
}

func NewStatService(
	queries *db.Queries,
) (core.StatService, error) {
	return &statService{
		queries,
	}, nil
}

func (s *statService) Get(
	ctx context.Context,
) (*core.Stats, error) {
	stats, err := s.queries.GetStats(ctx)
	if err != nil {
		return nil, err
	}

	return s.dbStatsIntoCore(stats), nil
}

func (s *statService) dbStatsIntoCore(stats db.GetStatsRow) *core.Stats {
	return &core.Stats{
		BytesStored: int(stats.BytesStored),
		BlobCount:   int(stats.BlobCount),
		PubkeyCount: int(stats.PubkeyCount),
	}
}
