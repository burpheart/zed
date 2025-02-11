package lakemanage

import (
	"context"
	"time"

	"github.com/brimdata/zed/api"
	lakeapi "github.com/brimdata/zed/lake/api"
	"github.com/brimdata/zed/lake/index"
	"github.com/brimdata/zed/lake/pools"
	"github.com/brimdata/zed/lakeparse"
	"github.com/segmentio/ksuid"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type branch struct {
	compact CompactConfig
	index   IndexConfig
	lake    lakeapi.Interface
	logger  *zap.Logger
	pool    *pools.Config
	name    string
	tasks   []branchTask
}

func newBranch(c Config, pool *pools.Config, indexes []index.Rule, lake lakeapi.Interface, logger *zap.Logger) (*branch, error) {
	branchName, compact, index, err := c.poolConfig(pool, indexes)
	if err != nil {
		return nil, err
	}
	b := &branch{
		compact: compact,
		index:   index,
		lake:    lake,
		logger: logger.Named("pool").With(
			zap.String("name", pool.Name),
			zap.Stringer("id", pool.ID),
			zap.String("branch", branchName),
		),
		pool: pool,
		name: branchName,
	}
	if !c.Compact.Disabled {
		b.tasks = append(b.tasks, &compactTask{b, b.logger.Named("compact")})
	}
	if c.Index.Enabled() {
		b.tasks = append(b.tasks, &indexTask{b, b.logger.Named("index")})
	}
	return b, nil
}

func (b *branch) head(ctx context.Context) (ksuid.KSUID, error) {
	branch, err := lakeapi.LookupBranchByName(ctx, b.lake, b.pool.Name, b.name)
	if err != nil {
		return ksuid.Nil, err
	}
	return branch.Branch.Commit, nil
}

func (b *branch) MarshalLogObject(o zapcore.ObjectEncoder) error {
	o.AddObject("compact", &b.compact)
	o.AddObject("index", &b.index)
	return nil
}

type branchTask interface {
	run(context.Context, ksuid.KSUID) (*time.Time, error)
	logger() *zap.Logger
}

type compactTask struct {
	*branch
	log *zap.Logger
}

func (b *compactTask) run(ctx context.Context, at ksuid.KSUID) (*time.Time, error) {
	b.log.Debug("compaction started")
	head := lakeparse.Commitish{Pool: b.pool.Name, Branch: at.String()}
	it, err := NewPoolDataObjectIterator(ctx, b.lake, &head, b.pool.Layout)
	if err != nil {
		return nil, err
	}
	defer it.Close()
	var nextcold *time.Time
	ch := make(chan Run)
	go func() {
		nextcold, err = CompactionScan(ctx, it, b.pool, b.compact.coldThreshold(), ch)
		close(ch)
	}()
	var found int
	var compacted int
	for run := range ch {
		commit, err := b.lake.Compact(ctx, b.pool.ID, b.name, run.ObjectIDs(), api.CommitMessage{})
		if err != nil {
			return nil, err
		}
		found++
		compacted += len(run.Objects)
		b.log.Debug("compacted", zap.Stringer("commit", commit), zap.Int("objects_compacted", len(run.Objects)))
	}
	level := zap.InfoLevel
	if compacted == 0 {
		level = zap.DebugLevel
	}
	b.log.Log(level, "compaction completed", zap.Int("runs_found", found), zap.Int("objects_compacted", compacted))
	return nextcold, err
}

func (c *compactTask) logger() *zap.Logger { return c.log }

type indexTask struct {
	*branch
	log *zap.Logger
}

func (b *indexTask) run(ctx context.Context, at ksuid.KSUID) (*time.Time, error) {
	b.log.Debug("index started")
	var nextcold *time.Time
	ch := make(chan ObjectIndexes)
	conf := b.index
	var err error
	go func() {
		nextcold, err = IndexScan(ctx, b.lake, b.pool.Name, at.String(), conf.coldThreshold(), conf.rules, ch)
		close(ch)
	}()
	var objects int
	var newindexes int
	for o := range ch {
		commit, err := b.lake.ApplyIndexRules(ctx, o.NeedsIndex, b.pool.ID, b.name, []ksuid.KSUID{o.Object.ID})
		if err != nil {
			return nil, err
		}
		objects++
		newindexes += len(o.NeedsIndex)
		b.log.Debug("indexed", zap.Stringer("commit", commit), zap.Stringer("object", o.Object.ID), zap.Int("indexes_created", len(o.NeedsIndex)))
	}
	level := zap.InfoLevel
	if objects == 0 {
		level = zap.DebugLevel
	}
	b.log.Log(level, "index completed", zap.Int("objects_indexed", objects), zap.Int("indexes_created", newindexes))
	return nextcold, err
}

func (c *indexTask) logger() *zap.Logger { return c.log }
