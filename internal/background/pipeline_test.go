package background

import (
	"context"
	"testing"
)

type corruptTools struct {
	testPipelineTools
	cancel    context.CancelFunc
	malformed bool
}

func (c corruptTools) CallPipelineTool(ctx context.Context, p PipelineContext, n string, in, out any) error {
	if n == BuildTool && c.malformed {
		*out.(*Aggregate) = Aggregate{ProductsCount: -1}
		return nil
	}
	e := c.testPipelineTools.CallPipelineTool(ctx, p, n, in, out)
	if n == BuildTool && c.cancel != nil {
		c.cancel()
	}
	return e
}
func TestPipelineCancellationBeforeSaveAndInvalidAggregate(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		s, _, _, _, u, w := fixture(t)
		s.WB.Tools = corruptTools{testPipelineTools{s.WB}, nil, malformed}
		ctx, cancel := context.WithCancel(context.Background())
		if !malformed {
			s.WB.Tools = corruptTools{testPipelineTools{s.WB}, cancel, false}
		}
		_, _ = s.Create(u, w)
		if e := s.RunNow(ctx, u, w); e != nil {
			t.Fatal(e)
		}
		cancel()
		if scalar(t, s, `SELECT COUNT(*) FROM wb_daily_snapshots`) != 0 {
			t.Fatal("invalid/cancelled save")
		}
	}
}
